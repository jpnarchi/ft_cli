// Workflow actions: list, get (redacted), create (--apply), update (--confirm).
//
// From the dashboard bundle (2026-10-21):
//
//	create : POST /workflow-builder/action          {action_name, action_description, action_type, action_fields}
//	update : PUT  /workflow-builder/action/{id}     same body; 409 + payload.blocked_agents when agents would break
//	impact : POST /workflow-builder/action/{id}/update-impact  {action_fields} → {enabled, references}
//
// Actions have no draft: an update is live for every agent that uses it, so it
// is gated like publish. Secrets never leave redaction (see redact.go).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const epAction = "/dashboard-controls/solve/v2/workflow-builder/action"

// actionBodyKeys is what the UI sends on create/update.
var actionBodyKeys = []string{"action_name", "action_description", "action_type", "action_fields", "is_background_task"}

func listActions(c creds) ([]map[string]any, error) {
	b, err := apiGet(c, epActions)
	if err != nil {
		return nil, err
	}
	var root struct {
		Actions []map[string]any `json:"actions"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("unexpected actions response: %w", err)
	}
	sort.Slice(root.Actions, func(i, j int) bool {
		return strings.ToLower(field(root.Actions[i], "action_name")) < strings.ToLower(field(root.Actions[j], "action_name"))
	})
	return root.Actions, nil
}

func resolveAction(c creds, q string) (map[string]any, error) {
	if q == "" {
		return nil, fmt.Errorf("action name or id required (see ft actions list)")
	}
	as, err := listActions(c)
	if err != nil {
		return nil, err
	}
	var subs []map[string]any
	for _, a := range as {
		if field(a, "action_id") == q || strings.EqualFold(field(a, "action_name"), q) {
			return a, nil
		}
		if strings.Contains(strings.ToLower(field(a, "action_name")), strings.ToLower(q)) {
			subs = append(subs, a)
		}
	}
	if len(subs) == 1 {
		return subs[0], nil
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("no action matches %q", q)
	}
	var names []string
	for _, s := range subs {
		names = append(names, field(s, "action_name"))
	}
	return nil, fmt.Errorf("%q matches %d actions: %s", q, len(subs), strings.Join(names, "; "))
}

func actionBody(a map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range actionBodyKeys {
		if v, ok := a[k]; ok {
			out[k] = v
		}
	}
	return out
}

func cmdActionsList(c creds, o opts) {
	as, err := listActions(c)
	if err != nil {
		fail(err)
	}
	if o.has("json", "raw") {
		r, n := redact(map[string]any{"actions": toAny(as)})
		fmt.Println(jsonText(r))
		if n > 0 {
			fmt.Fprintf(os.Stderr, "(%d secret value(s) redacted)\n", n)
		}
		return
	}
	fmt.Printf("%-38s %-40s %s\n", "ID", "NAME", "TYPE")
	for _, a := range as {
		fmt.Printf("%-38s %-40s %s\n", field(a, "action_id"), trunc(field(a, "action_name"), 40), field(a, "action_type"))
	}
	fmt.Fprintf(os.Stderr, "\n%d actions. Details: ft action get <name|id>\n", len(as))
}

func toAny(ms []map[string]any) []any {
	out := make([]any, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}

func cmdActionGet(c creds, o opts) {
	a, err := resolveAction(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	r, n := redact(a)
	out := jsonText(r)
	if f := o.val("out", "o"); f != "" {
		if err := os.WriteFile(f, []byte(jsonText(actionBody(r.(map[string]any)))+"\n"), 0o600); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s (%d secret(s) shown as %s; leave them as is to keep them)\nthen: ft action update %q --file %s\n", f, n, redactedMark, field(a, "action_name"), f)
		return
	}
	fmt.Println(out)
	if n > 0 {
		fmt.Fprintf(os.Stderr, "(%d secret value(s) redacted)\n", n)
	}
}

// readActionFile loads a create/update body and checks the basic shape.
func readActionFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if field(m, "action_name") == "" || field(m, "action_type") == "" {
		return nil, fmt.Errorf("%s needs at least action_name and action_type (start from `ft action get … --out`)", path)
	}
	if _, ok := m["action_fields"].(map[string]any); !ok {
		return nil, fmt.Errorf("%s: action_fields must be an object", path)
	}
	return actionBody(m), nil
}

func cmdActionCreate(c creds, o opts) {
	file := o.val("file", "f")
	if file == "" {
		fail(fmt.Errorf("usage: ft action create --file action.json [--apply]   (secrets as ${env:NAME})"))
	}
	body, err := readActionFile(file)
	if err != nil {
		fail(err)
	}
	if strings.Contains(jsonText(body), redactedMark) {
		fail(fmt.Errorf("%s contains %s, which only works for updates; give new secrets as ${env:NAME}", file, redactedMark))
	}
	as, err := listActions(c)
	if err != nil {
		fail(err)
	}
	for _, a := range as {
		if strings.EqualFold(field(a, "action_name"), field(body, "action_name")) {
			fail(fmt.Errorf("an action named %q already exists (%s); use ft action update", field(a, "action_name"), field(a, "action_id")))
		}
	}
	sendable, envs, err := resolveEnvRefs(body)
	if err != nil {
		fail(err)
	}
	shown, _ := redact(body)
	fmt.Printf("Create action %q (%s)\n\n%s\n", field(body, "action_name"), field(body, "action_type"), jsonText(shown))
	if len(envs) > 0 {
		fmt.Printf("\nsecrets from env: %s\n", strings.Join(envs, ", "))
	}
	if !o.has("apply") {
		dryRunNote("action create")
		return
	}
	b, err := apiSend(c, "POST", epAction, sendable)
	if err != nil {
		fail(err)
	}
	id := ""
	for _, a := range getArr(getMap(decodeJSON(b)), "actions") {
		if m, ok := a.(map[string]any); ok && strings.EqualFold(field(m, "action_name"), field(body, "action_name")) {
			id = field(m, "action_id")
		}
	}
	fmt.Printf("✅ action created %s. It isn't used by any agent until you add it in the builder.\n", orDash(id))
}

func cmdActionUpdate(c creds, o opts) {
	file := o.val("file", "f")
	if file == "" {
		fail(fmt.Errorf("usage: ft action update <name|id> --file action.json [--confirm]   (start from `ft action get … --out action.json`)"))
	}
	cur, err := resolveAction(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	id, name := field(cur, "action_id"), field(cur, "action_name")
	next, err := readActionFile(file)
	if err != nil {
		fail(err)
	}
	if field(next, "action_type") != field(cur, "action_type") {
		fail(fmt.Errorf("action_type can't change (%s → %s); create a new action instead", field(cur, "action_type"), field(next, "action_type")))
	}
	curBody := actionBody(cur)
	restored, err := restoreSecrets(any(next), any(curBody))
	if err != nil {
		fail(err)
	}
	sendable, envs, err := resolveEnvRefs(restored)
	if err != nil {
		fail(err)
	}
	before, _ := redact(curBody)
	after, _ := redact(next)
	diff := lineDiff(jsonText(before), jsonText(after))
	fmt.Printf("Update action %q [%s] — LIVE for every agent that uses it\n\n%s\n", name, id, diff)
	if len(envs) > 0 {
		fmt.Printf("\nsecrets from env: %s\n", strings.Join(envs, ", "))
	}
	if jsonText(sendable) == jsonText(curBody) {
		return
	}
	sm := sendable.(map[string]any)
	if b, err := apiSend(c, "POST", epAction+"/"+id+"/update-impact", map[string]any{"action_fields": sm["action_fields"]}); err != nil {
		fmt.Fprintf(os.Stderr, "\n⚠️  impact check failed (%v); the UI runs this before saving, so look in the builder first\n", err)
	} else {
		printImpact(decodeJSON(b))
	}
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNo change made. Actions have no draft: this goes live at once. Re-run with --confirm once it's approved.")
		return
	}
	bk := backup("action", name, before)
	if _, err := apiSend(c, "PUT", epAction+"/"+id, sm); err != nil {
		if strings.Contains(err.Error(), "409") {
			fail(fmt.Errorf("FT blocked the update because some agents would break: %w", err))
		}
		fail(err)
	}
	fmt.Printf("✅ action %q updated. Backup (secrets redacted): %s\n", name, bk)
}

func printImpact(v any) {
	m := getMap(v)
	if m == nil {
		return
	}
	refs := getArr(m, "references")
	fmt.Printf("\nimpact check: enabled=%s, %d reference(s)\n", orDash(field(m, "enabled")), len(refs))
	for _, r := range refs {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		var errs []string
		for _, e := range getArr(rm, "errors") {
			errs = append(errs, fmt.Sprint(e))
		}
		line := "  • " + orDash(field(rm, "name", "intent_name", "source_name", "source_id"))
		if len(errs) > 0 {
			line += " — " + strings.Join(errs, "; ")
		}
		fmt.Println(line)
	}
}
