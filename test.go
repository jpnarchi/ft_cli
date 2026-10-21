// Testing commands: run an email preview across several turns and read its
// logs — the "2-message test" from the daily workflow, scripted.
//
// NOTE: the preview endpoint + body are placeholders pending a browser capture
// (see endpoints_write.go). Once confirmed, only the constants there change.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// apiSend performs an authenticated JSON POST/PUT and returns the body.
func apiSend(c creds, method, path string, body any) ([]byte, error) {
	if dashboardAPI == "" {
		return nil, fmt.Errorf("set FT_API_BASE in ~/.ft_cli_creds")
	}
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	return apiSendRaw(c, method, path, "application/json", raw)
}

// apiSendRaw sends raw bytes with a given content type (JSON or multipart).
func apiSendRaw(c creds, method, path, contentType string, raw []byte) ([]byte, error) {
	tok, err := ensureToken(c)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if raw != nil {
		rdr = bytes.NewReader(raw)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, _ := http.NewRequest(method, dashboardAPI+path, rdr)
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("HTTP %d: not authorized; run `ft login`", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %.400s", resp.StatusCode, b)
	}
	return b, nil
}

type previewLog struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	LogType string `json:"log_type"`
}

func cmdPreview(c creds, args []string) {
	o := parseOpts(args)
	msgs := o.thread
	realActions := o.has("real", "real-actions") // default: mock actions so preview never hits Zendesk
	if len(msgs) == 0 {
		fail(fmt.Errorf(`usage: ft preview <agent|auto> --channel email --thread "msg 1" "msg 2" … [--real] [--full]`))
	}
	if ch := o.channel(); ch != "email" {
		fail(fmt.Errorf("preview on %q isn't supported: the widget preview runs on the widget's own API key, which this CLI deliberately never handles. Use the UI preview for widget", ch))
	}
	var a agent
	if q := o.agentArg(); q != "" && !strings.EqualFold(q, "auto") {
		var err error
		if a, err = resolveAgent(c, q); err != nil {
			fail(err)
		}
	}
	full = o.has("full")

	if realActions {
		// Known FT gotcha: the "Create real tickets" toggle does NOT stop custom
		// actions from firing. --real lets them fire (may create live tickets).
		fmt.Fprintf(os.Stderr, "⚠️  --real: custom actions WILL fire and may create real tickets / side effects.\n")
		if !confirm("Run preview with real actions?") {
			fmt.Fprintln(os.Stderr, "cancelled")
			return
		}
	} else {
		fmt.Fprintln(os.Stderr, "⚠️  asked FT to mock actions, but that flag is NOT confirmed yet: FT may still run them.\n   Don't preview agents whose actions write (e.g. create a Zendesk ticket) until mocking is verified.")
	}

	conv, err := startPreview(c, a, msgs[0], !realActions)
	if err != nil {
		fail(err)
	}
	printTurn(1, msgs[0], conv)
	for i, m := range msgs[1:] {
		conv, err = continuePreview(c, conv.ID, m, !realActions)
		if err != nil {
			fail(err)
		}
		printTurn(i+2, m, conv)
	}
	fmt.Printf("\npreview session: %s\n", conv.ID)
}

// full prints every log message untruncated (preview --full).
var full bool

type previewResult struct {
	ID    string
	Reply string
	Logs  []previewLog
	Raw   map[string]any
}

// summarize extracts the salient signals from a preview turn's logs.
func (p previewResult) intent() string { return p.logFind("Intent Predicted", "Intent identified:") }
func (p previewResult) fellBack() bool { return p.logHas("Executing Fallback Workflow") }

func (p previewResult) logHas(title string) bool {
	for _, l := range p.Logs {
		if strings.Contains(l.Title, title) {
			return true
		}
	}
	return false
}
func (p previewResult) logFind(title, prefix string) string {
	for _, l := range p.Logs {
		if strings.Contains(l.Title, title) {
			return strings.TrimSpace(strings.TrimPrefix(l.Message, prefix))
		}
	}
	return ""
}

func printTurn(n int, msg string, p previewResult) {
	fmt.Printf("\n─ turn %d ─ you: %s\n", n, trunc(msg, 80))
	if it := p.intent(); it != "" {
		fmt.Printf("  intent:   %s\n", it)
	}
	fmt.Printf("  fallback: %v\n", p.fellBack())
	for _, l := range p.Logs {
		if strings.Contains(l.Title, "Workflow Action") {
			first := strings.SplitN(l.Message, "\n", 2)[0]
			fmt.Printf("  action:   %s\n", trunc(strings.TrimPrefix(first, "Executed action: "), 90))
		}
	}
	if p.Reply != "" {
		fmt.Printf("  reply:    %s\n", trunc(strings.ReplaceAll(p.Reply, "\n", " "), 200))
	}
	printLogs(p.Logs)
}

// printLogs shows the trace the team used to paste by hand: intent, coaching
// applied, tool calls with inputs/outputs, replies.
func printLogs(logs []previewLog) {
	if len(logs) == 0 {
		return
	}
	fmt.Println("  logs:")
	for _, l := range logs {
		msg := strings.TrimSpace(l.Message)
		if !full {
			msg = trunc(strings.ReplaceAll(msg, "\n", " ⏎ "), 240)
		}
		title := l.Title
		if title == "" {
			title = l.LogType
		}
		if msg == "" {
			fmt.Printf("    • %s\n", title)
		} else {
			fmt.Printf("    • %s: %s\n", title, msg)
		}
	}
}

func parsePreview(b []byte) previewResult {
	var r previewResult
	json.Unmarshal(b, &r.Raw)
	r.ID = field(r.Raw, "preview_session_id", "conversation_id", "id")
	r.Reply = field(r.Raw, "response", "reply", "message")
	if lb, err := json.Marshal(r.Raw["logs"]); err == nil {
		json.Unmarshal(lb, &r.Logs)
	}
	return r
}
