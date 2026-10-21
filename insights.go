// Metrics and conversation logs.
//
//	metrics   : GET  /dashboard-controls/solve/v2/workflow-builder/metrics/breakdown?channel&start_timestamp&end_timestamp
//	logs get  : GET  /dashboard-controls/solve-insights/v0/conversation-detail?conversation_id=…
//	logs list : POST /dashboard-controls/solve-insights/v0/chats-table {page, page_size, …filters}
package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const insights = "/dashboard-controls/solve-insights/v0"

// window parses --from/--to (YYYY-MM-DD) or --since, default last 7 days.
func window(o opts) (time.Time, time.Time) {
	to := time.Now()
	from := to.AddDate(0, 0, -7)
	parse := func(s string) (time.Time, bool) {
		for _, f := range []string{"2006-01-02", "2006-01-02T15:04", time.RFC3339} {
			if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	if t, ok := parse(o.val("from", "since")); ok {
		from = t
	}
	if t, ok := parse(o.val("to")); ok {
		to = t.Add(24*time.Hour - time.Second)
	}
	return from, to
}

// rowsOf finds the first array of objects in a response.
func rowsOf(v any) []map[string]any {
	var out []map[string]any
	var walk func(any) bool
	walk = func(x any) bool {
		switch t := x.(type) {
		case []any:
			for _, y := range t {
				if m, ok := y.(map[string]any); ok {
					out = append(out, m)
				}
			}
			if len(out) > 0 {
				return true
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if walk(t[k]) {
					return true
				}
			}
		}
		return false
	}
	walk(v)
	return out
}

func rowMatchesAgent(m map[string]any, a *agent) bool {
	if a == nil {
		return true
	}
	blob := strings.ToLower(jsonText(m))
	return strings.Contains(blob, strings.ToLower(a.ID)) || strings.Contains(blob, strings.ToLower(a.Name)) ||
		(a.Workflow != "" && strings.Contains(blob, strings.ToLower(a.Workflow)))
}

func optAgent(c creds, o opts) *agent {
	q := o.val("agent")
	if q == "" {
		q = o.agentArg()
	}
	if q == "" {
		return nil
	}
	a, err := resolveAgent(c, q)
	if err != nil {
		fail(err)
	}
	return &a
}

func cmdMetrics(c creds, o opts) {
	a := optAgent(c, o)
	from, to := window(o)
	q := url.Values{}
	q.Set("channel", o.channel())
	q.Set("start_timestamp", fmt.Sprint(from.Unix()))
	q.Set("end_timestamp", fmt.Sprint(to.Unix()))
	b, err := apiGet(c, "/dashboard-controls/solve/v2/workflow-builder/metrics/breakdown?"+q.Encode())
	if err != nil {
		fail(err)
	}
	if o.has("raw", "json") {
		fmt.Println(prettyJSON(b))
		return
	}
	fmt.Printf("%s metrics, %s → %s\n\n", o.channel(), from.Format("2006-01-02"), to.Format("2006-01-02"))
	n := 0
	for _, r := range rowsOf(decodeJSON(b)) {
		if !rowMatchesAgent(r, a) {
			continue
		}
		name := field(r, "intent_name", "workflow_name", "name", "intent_title")
		var parts []string
		keys := make([]string, 0, len(r))
		for k := range r {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch r[k].(type) {
			case float64:
				parts = append(parts, fmt.Sprintf("%s=%v", k, r[k]))
			}
		}
		fmt.Printf("## %s\n  %s\n", orDash(name), strings.Join(parts, "  "))
		n++
	}
	if n == 0 {
		fmt.Println("no rows (try --raw to see the response)")
	}
}

func cmdLogsGet(c creds, o opts) {
	if len(o.pos) == 0 {
		fail(fmt.Errorf("usage: ft logs get <conversation_id>"))
	}
	full = o.has("full")
	b, err := apiGet(c, insights+"/conversation-detail?conversation_id="+url.QueryEscape(o.pos[0]))
	if err != nil {
		fail(err)
	}
	if o.has("raw", "json") {
		fmt.Println(prettyJSON(b))
		return
	}
	root := decodeJSON(b)
	m, _ := root.(map[string]any)
	var logs []previewLog
	for _, x := range getArr(m, "logs", "events", "conversation_logs") {
		if lm, ok := x.(map[string]any); ok {
			logs = append(logs, previewLog{Title: field(lm, "title", "event", "type"), Message: field(lm, "message", "text", "content"), LogType: field(lm, "log_type")})
		}
	}
	if len(logs) == 0 {
		fmt.Println(prettyJSON(b))
		return
	}
	fmt.Printf("conversation %s\n", o.pos[0])
	printLogs(logs)
}

func cmdLogsList(c creds, o opts) {
	a := optAgent(c, o)
	from, to := window(o)
	body := map[string]any{
		"page": 1, "page_size": 100,
		"start_timestamp": from.Unix(), "end_timestamp": to.Unix(),
		"start_date": from.Format("2006-01-02"), "end_date": to.Format("2006-01-02"),
		"channel": o.channel(),
	}
	b, err := apiSend(c, "POST", insights+"/chats-table", body)
	if err != nil {
		fail(err)
	}
	if o.has("raw", "json") {
		fmt.Println(prettyJSON(b))
		return
	}
	n := 0
	for _, r := range rowsOf(decodeJSON(b)) {
		if !rowMatchesAgent(r, a) {
			continue
		}
		fmt.Printf("%s  %s  %s  %s\n",
			field(r, "conversation_id", "chat_id", "id"),
			field(r, "created_date", "timestamp", "created_at", "start_time"),
			trunc(field(r, "intent_name", "workflow_name", "intent"), 34),
			field(r, "is_deflected", "deflected", "status", "outcome"))
		n++
	}
	fmt.Printf("\n%d conversations (ft logs get <id> for the trace)\n", n)
}
