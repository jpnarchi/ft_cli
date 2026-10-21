// Draft writes (diff first; --apply writes; backup before every write) and the
// gated live commands (publish, channel on/off need --confirm).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- set-policy ----------

func cmdSetPolicy(c creds, o opts) {
	file := o.val("file", "f")
	if file == "" {
		fail(fmt.Errorf("usage: ft agent set-policy <agent> --channel email --file policy.md [--apply]"))
	}
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	newText, err := os.ReadFile(file)
	if err != nil {
		fail(err)
	}
	v, err := writableDraft(c, a)
	if err != nil {
		fail(err)
	}
	ap, err := getAutopilot(c, v)
	if err != nil {
		fail(err)
	}
	curHTML := field(ap, "policy_description")
	curText, cm := policyToText(curHTML)
	// chips from a previous `agent get --out` fill tokens the draft no longer has
	if side, err := os.ReadFile(file + ".chips.json"); err == nil {
		var extra chipMap
		if json.Unmarshal(side, &extra) == nil {
			for k, el := range extra {
				if _, ok := cm[k]; !ok {
					cm[k] = el
				}
			}
		}
	}
	var targets []mentionTarget
	if needsNewChips(string(newText), cm) {
		targets = mentionTargets(c)
	}
	// safety: the unchanged policy must survive text → HTML byte for byte,
	// otherwise a write could change markup the user never touched
	if same, err := textToPolicy(curText, cm, nil); err != nil {
		fail(fmt.Errorf("can't round-trip %s's current policy (%v); edit it in the UI", a.Name, err))
	} else if back, _, _ := preserveBlocks(curHTML, same); back != curHTML {
		fail(fmt.Errorf("can't round-trip %s's current policy losslessly, so a write could change parts you didn't edit; edit it in the UI", a.Name))
	}
	genHTML, err := textToPolicy(string(newText), cm, targets)
	if err != nil {
		fail(err)
	}
	newHTML, kept, rebuilt := preserveBlocks(curHTML, genHTML)
	rendered, _ := policyToText(newHTML)
	fmt.Printf("%s — email draft v%s\n\n%s\n", a.Name, v.Version, lineDiff(curText, rendered))
	if rendered == curText {
		return
	}
	fmt.Printf("\n%d block(s) kept byte for byte, %d rebuilt\n", kept, rebuilt)
	if !o.has("apply") {
		dryRunNote("set-policy")
		return
	}
	bk := backup("policy", a.Name, map[string]any{"version": v.Raw, "autopilot_workflow": ap})
	if err := apwPut(c, ap, "policy-description", map[string]any{"policy_description": newHTML}); err != nil {
		fail(err)
	}
	// verify
	if ap2, err := getAutopilot(c, v); err == nil {
		after, _ := policyToText(field(ap2, "policy_description"))
		if after != rendered {
			fmt.Fprintln(os.Stderr, "⚠️  saved, but the stored policy differs from what was sent (FT may have normalized it); re-run `ft agent get` to review")
		}
	}
	fmt.Printf("\n✅ draft policy updated (v%s). Backup: %s\nPublish with: ft publish %q --channel email --confirm\n", v.Version, bk, a.Name)
}

func needsNewChips(text string, cm chipMap) bool {
	for _, t := range reToken.FindAllString(text, -1) {
		if _, ok := cm[t]; !ok {
			return true
		}
	}
	return false
}

// ---------- set-setting ----------

func cmdSetSetting(c creds, o opts) {
	var name []string
	settings := map[string]string{}
	for _, p := range o.pos {
		if k, val, ok := strings.Cut(p, "="); ok {
			settings[strings.ToLower(k)] = val
		} else {
			name = append(name, p)
		}
	}
	if len(settings) == 0 {
		fail(fmt.Errorf("usage: ft agent set-setting <agent> --channel email response_limit=3 [bypass_fallback_triggers=true] [--apply]"))
	}
	a, err := resolveAgent(c, strings.Join(name, " "))
	if err != nil {
		fail(err)
	}
	v, err := writableDraft(c, a)
	if err != nil {
		fail(err)
	}
	ap, err := getAutopilot(c, v)
	if err != nil {
		fail(err)
	}
	type change struct {
		key, from, to, suffix string
		body                  map[string]any
	}
	var changes []change
	for k, val := range settings {
		switch k {
		case "response_limit", "max_responses":
			var n any
			if val == "off" || val == "none" || val == "0" {
				n = nil
			} else if i, err := strconv.Atoi(val); err == nil {
				n = i
			} else {
				fail(fmt.Errorf("response_limit must be a number or off"))
			}
			changes = append(changes, change{"response_limit", orDash(field(ap, "max_responses")), val, "max-responses", map[string]any{"max_responses": n}})
		case "bypass_fallback_triggers":
			b := val == "true" || val == "on" || val == "yes"
			changes = append(changes, change{k, orDash(field(ap, "bypass_fallback_triggers")), fmt.Sprint(b), "bypass-fallback-triggers", map[string]any{"bypass_fallback_triggers": b}})
		default:
			fail(fmt.Errorf("unknown setting %q (supported: response_limit, bypass_fallback_triggers)", k))
		}
	}
	fmt.Printf("%s — email draft v%s\n", a.Name, v.Version)
	var real []change
	for _, ch := range changes {
		if ch.from == ch.to || (ch.from == "-" && (ch.to == "off" || ch.to == "none" || ch.to == "0")) {
			fmt.Printf("  %s: already %s\n", ch.key, ch.from)
			continue
		}
		fmt.Printf("  %s: %s → %s\n", ch.key, ch.from, ch.to)
		real = append(real, ch)
	}
	if changes = real; len(changes) == 0 {
		return
	}
	if !o.has("apply") {
		dryRunNote("set-setting")
		return
	}
	bk := backup("settings", a.Name, ap)
	for _, ch := range changes {
		if err := apwPut(c, ap, ch.suffix, ch.body); err != nil {
			fail(fmt.Errorf("%s: %w", ch.key, err))
		}
	}
	fmt.Printf("✅ draft settings updated. Backup: %s\n", bk)
}

// ---------- search sources ----------

// toolConfig is a tool's settings: GET returns them as tool_fields, while the
// PUT body names them configuration_fields.
func toolConfig(t map[string]any) any {
	if v, ok := t["configuration_fields"]; ok && v != nil {
		return v
	}
	return t["tool_fields"]
}

func searchTools(ap map[string]any) []map[string]any {
	var out []map[string]any
	for _, t := range getArr(ap, "tools", "tool_definitions") {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		blob := strings.ToLower(field(m, "tool_name", "name", "tool_type", "type", "display_name"))
		if strings.Contains(blob, "search") || strings.Contains(blob, "article") || strings.Contains(blob, "knowledge") || strings.Contains(blob, "help") {
			out = append(out, m)
		}
	}
	return out
}

func cmdSearchSourcesGet(c creds, o opts) {
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	v, err := selectVersion(c, a, o)
	if err != nil {
		fail(err)
	}
	ap, err := getAutopilot(c, v)
	if err != nil {
		fail(err)
	}
	ts := searchTools(ap)
	if len(ts) == 0 {
		fmt.Printf("%s v%s has no search action.\n", a.Name, v.Version)
		return
	}
	for _, t := range ts {
		cf := toolConfig(t)
		rcf, _ := redact(cf)
		js := jsonText(rcf)
		public := strings.Contains(strings.ToLower(js), "public")
		github := strings.Contains(strings.ToLower(js), "github")
		fmt.Printf("## %s  [tool %s]\npublic-only filter present: %v   GitHub source present: %v\n%s\n\n",
			field(t, "tool_name", "name", "display_name"), field(t, "tool_id", "id"), public, github, js)
	}
}

func cmdSearchSourcesSet(c creds, o opts) {
	file := o.val("file", "f")
	if file == "" {
		fail(fmt.Errorf("usage: ft search-sources set <agent> --file fields.json [--tool ID] [--apply]   (start from `ft search-sources get`)"))
	}
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		fail(err)
	}
	var newCF any
	if err := json.Unmarshal(raw, &newCF); err != nil {
		fail(fmt.Errorf("%s: %w", file, err))
	}
	if m, ok := newCF.(map[string]any); ok && m["configuration_fields"] != nil {
		newCF = m["configuration_fields"]
	}
	v, err := writableDraft(c, a)
	if err != nil {
		fail(err)
	}
	ap, err := getAutopilot(c, v)
	if err != nil {
		fail(err)
	}
	ts := searchTools(ap)
	var tool map[string]any
	for _, t := range ts {
		if id := o.val("tool"); id == "" || field(t, "tool_id", "id") == id {
			tool = t
			break
		}
	}
	if tool == nil || (len(ts) > 1 && o.val("tool") == "") {
		fail(fmt.Errorf("pick the search tool with --tool (found %d)", len(ts)))
	}
	shownCur, _ := redact(toolConfig(tool))
	shownNew, _ := redact(newCF)
	// [redacted] values from `search-sources get` keep the current secret
	if newCF, err = restoreSecrets(newCF, toolConfig(tool)); err != nil {
		fail(err)
	}
	fmt.Printf("%s — draft v%s, tool %s\n\n%s\n", a.Name, v.Version, field(tool, "tool_id", "id"), lineDiff(jsonText(shownCur), jsonText(shownNew)))
	if !o.has("apply") {
		dryRunNote("search-sources set")
		return
	}
	bk := backup("search_sources", a.Name, tool)
	if err := apwPut(c, ap, "tools/"+field(tool, "tool_id", "id")+"/configuration-fields", map[string]any{"configuration_fields": newCF}); err != nil {
		fail(err)
	}
	fmt.Printf("✅ search sources updated on the draft. Backup: %s\n", bk)
}

// ---------- set-intent ----------

func cmdSetIntent(c creds, o opts) {
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	desc, phrases, raw := intentInfo(c, a)
	if raw == nil {
		fail(fmt.Errorf("could not read intent %s", a.Name))
	}
	newName, newDesc, newPhrases := a.Name, desc, phrases
	if v := o.val("name"); v != "" {
		newName = v
	}
	if v := o.val("description", "desc"); v != "" {
		if b, err := os.ReadFile(v); err == nil {
			v = string(b)
		}
		newDesc = strings.TrimSpace(v)
	}
	if f := o.val("phrases"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			fail(err)
		}
		newPhrases = nil
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "*")); l != "" {
				newPhrases = append(newPhrases, l)
			}
		}
	}
	for _, r := range o.multi["remove-phrase"] {
		var kept []string
		found := false
		for _, p := range newPhrases {
			if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(r)) {
				found = true
				continue
			}
			kept = append(kept, p)
		}
		if !found {
			fail(fmt.Errorf("no training phrase %q on %s (see ft agent get)", r, a.Name))
		}
		newPhrases = kept
	}
	for _, add := range o.multi["add-phrase"] {
		add = strings.TrimSpace(add)
		dup := false
		for _, p := range newPhrases {
			if strings.EqualFold(strings.TrimSpace(p), add) {
				dup = true
			}
		}
		if dup {
			fmt.Fprintf(os.Stderr, "(%q is already a training phrase; skipped)\n", add)
		} else if add != "" {
			newPhrases = append(newPhrases, add)
		}
	}
	before := fmt.Sprintf("name: %s\ndescription:\n%s\nphrases:\n%s", a.Name, desc, strings.Join(phrases, "\n"))
	after := fmt.Sprintf("name: %s\ndescription:\n%s\nphrases:\n%s", newName, newDesc, strings.Join(newPhrases, "\n"))
	fmt.Println(lineDiff(before, after))
	if before == after {
		return
	}
	// the Edit Agent dialog has no draft: name, description and phrases change
	// routing for every live channel as soon as they're saved
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNo change made. This is live at once (no draft; it changes routing on every channel). Re-run with --confirm once it's approved.")
		return
	}
	bk := backup("intent", a.Name, raw)
	if _, err := apiSend(c, "PUT", intentPath(a.ID), map[string]any{"name": newName, "description": newDesc, "phrases": newPhrases}); err != nil {
		fail(err)
	}
	fmt.Printf("✅ intent updated. Backup: %s\n", bk)
}

// ---------- versions ----------

func cmdAgentVersions(c creds, o opts) {
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	if o.channel() != "email" {
		cmdVersions(c, a.Name)
		return
	}
	vs, err := emailVersions(c, a)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%-6s %-10s %-28s %-26s %s\n", "VER", "STATE", "NAME", "LAST EDITED", "BY")
	for i := len(vs) - 1; i >= 0; i-- {
		v := vs[i]
		st := "draft"
		if v.Published {
			st = "PUBLISHED"
		} else if !v.Draft {
			st = "saved"
		}
		fmt.Printf("%-6s %-10s %-28s %-26s %s\n", v.Version, st, trunc(orDash(v.Name), 28), v.LastModified, v.By)
	}
	if snaps, _ := filepath.Glob(filepath.Join(home(), "ft_versions", safeName(a.Name), "*.json")); len(snaps) > 0 {
		fmt.Println("\nlocal restore points (ft version save):")
		for _, s := range snaps {
			fmt.Println("  " + strings.TrimSuffix(filepath.Base(s), ".json"))
		}
	}
}

func cmdVersionSave(c creds, o opts) {
	name := o.val("name")
	if name == "" {
		fail(fmt.Errorf(`usage: ft version save <agent> --channel email --name "before tone edit"`))
	}
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	v, err := writableDraft(c, a)
	if err != nil {
		fail(err)
	}
	ap, err := getAutopilot(c, v)
	if err != nil {
		fail(err)
	}
	// local snapshot = a real restore point even if FT keeps editing this version
	dir := filepath.Join(home(), "ft_versions", safeName(a.Name))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(fmt.Errorf("restore point not saved: %w", err))
	}
	p := filepath.Join(dir, safeName(name)+".json")
	snap, _ := redact(map[string]any{"saved_at": time.Now().Format(time.RFC3339), "version": v.Raw, "autopilot_workflow": ap})
	if err := os.WriteFile(p, []byte(jsonText(snap)), 0o600); err != nil {
		fail(fmt.Errorf("restore point not saved: %w", err))
	}
	if _, err := apiSend(c, "PUT", fmt.Sprintf("%s/intent-email-configuration/%s/version/%s/name", emailBuilder, v.ConfigID, v.Version), map[string]any{"name": name}); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  named locally only; FT rename failed: %v\n", err)
	}
	fmt.Printf("✅ saved restore point %q (v%s). Local copy: %s\n", name, v.Version, p)
}

func cmdVersionRestore(c creds, o opts) {
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	var src map[string]any
	switch {
	case o.val("name") != "":
		b, err := os.ReadFile(filepath.Join(home(), "ft_versions", safeName(a.Name), safeName(o.val("name"))+".json"))
		if err != nil {
			fail(fmt.Errorf("no local restore point %q (see ft agent versions)", o.val("name")))
		}
		src = getMap(decodeJSON(b), "autopilot_workflow")
	case o.val("version") != "":
		v, err := selectVersion(c, a, o)
		if err != nil {
			fail(err)
		}
		if src, err = getAutopilot(c, v); err != nil {
			fail(err)
		}
	default:
		fail(fmt.Errorf(`usage: ft version restore <agent> --channel email (--version N | --name "…") [--apply]`))
	}
	d, err := writableDraft(c, a)
	if err != nil {
		fail(err)
	}
	dst, err := getAutopilot(c, d)
	if err != nil {
		fail(err)
	}
	curText, _ := policyToText(field(dst, "policy_description"))
	srcText, _ := policyToText(field(src, "policy_description"))
	fmt.Printf("%s — restoring into draft v%s\n\n## Policy\n%s\n\n## Settings\n  response_limit: %s → %s\n  bypass_fallback_triggers: %s → %s\n",
		a.Name, d.Version, lineDiff(curText, srcText),
		orDash(field(dst, "max_responses")), orDash(field(src, "max_responses")),
		orDash(field(dst, "bypass_fallback_triggers")), orDash(field(src, "bypass_fallback_triggers")))
	dstTools := map[string]map[string]any{}
	for _, t := range getArr(dst, "tools", "tool_definitions") {
		if m, ok := t.(map[string]any); ok {
			dstTools[field(m, "tool_id", "id")] = m
		}
	}
	var toolUpdates []map[string]any
	for _, t := range getArr(src, "tools", "tool_definitions") {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		id := field(m, "tool_id", "id")
		if cur, ok := dstTools[id]; !ok {
			fmt.Printf("  ⚠️  tool %s exists in the restore point but not in the draft — re-add it in the UI\n", field(m, "tool_name", "name"))
		} else if cfg, err := restoreSecrets(toolConfig(m), toolConfig(cur)); err != nil {
			fmt.Printf("  ⚠️  tool %s: %v — its configuration will not be restored\n", field(m, "tool_name", "name"), err)
		} else if jsonText(toolConfig(cur)) != jsonText(cfg) {
			fmt.Printf("  tool %s configuration will be restored\n", field(m, "tool_name", "name"))
			toolUpdates = append(toolUpdates, map[string]any{"tool_id": id, "tool_name": field(m, "tool_name", "name"), "configuration_fields": cfg})
		}
	}
	if !o.has("apply") {
		dryRunNote("version restore")
		return
	}
	bk := backup("restore", a.Name, dst)
	if err := apwPut(c, dst, "policy-description", map[string]any{"policy_description": field(src, "policy_description")}); err != nil {
		fail(err)
	}
	var mr any
	if s := field(src, "max_responses"); s != "" && s != "<nil>" {
		f, _ := strconv.ParseFloat(s, 64)
		mr = int(f)
	}
	var failed []string
	if err := apwPut(c, dst, "max-responses", map[string]any{"max_responses": mr}); err != nil {
		failed = append(failed, fmt.Sprintf("response_limit: %v", err))
	}
	if err := apwPut(c, dst, "bypass-fallback-triggers", map[string]any{"bypass_fallback_triggers": field(src, "bypass_fallback_triggers") == "true"}); err != nil {
		failed = append(failed, fmt.Sprintf("bypass_fallback_triggers: %v", err))
	}
	for _, t := range toolUpdates {
		if err := apwPut(c, dst, "tools/"+field(t, "tool_id", "id")+"/configuration-fields", map[string]any{"configuration_fields": toolConfig(t)}); err != nil {
			failed = append(failed, fmt.Sprintf("tool %s: %v", field(t, "tool_name", "name"), err))
		}
	}
	if len(failed) > 0 {
		fail(fmt.Errorf("policy restored into draft v%s, but these parts failed (backup: %s):\n  %s", d.Version, bk, strings.Join(failed, "\n  ")))
	}
	fmt.Printf("✅ restored into draft v%s. Backup: %s\n", d.Version, bk)
}

// ---------- publish (gated) ----------

func cmdPublish(c creds, o opts) {
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	if o.channel() != "email" {
		publishWorkflow(c, a, o)
		return
	}
	d, err := writableDraft(c, a)
	if err != nil {
		fail(err)
	}
	dap, err := getAutopilot(c, d)
	if err != nil {
		fail(err)
	}
	vs, _ := emailVersions(c, a)
	var pubText string
	var pubAP map[string]any
	if p, err := pickVersion(vs, true); err != nil {
		fmt.Fprintln(os.Stderr, "⚠️  no published version found; the diff below compares against an empty policy")
	} else if pubAP, err = getAutopilot(c, p); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  could not read the published v%s (%v); the diff below compares against an empty policy\n", p.Version, err)
	} else {
		pubText, _ = policyToText(field(pubAP, "policy_description"))
	}
	draftText, _ := policyToText(field(dap, "policy_description"))
	fmt.Printf("Publish %s email draft v%s → live\n\n## Policy (published → draft)\n%s\n\n## response_limit: %s → %s\n",
		a.Name, d.Version, lineDiff(pubText, draftText), orDash(field(pubAP, "max_responses")), orDash(field(dap, "max_responses")))
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNot published. This goes live to contributors: re-run with --confirm once it's approved for this agent.")
		return
	}
	bk := backup("prepublish", a.Name, map[string]any{"published": pubAP, "draft": dap})
	if _, err := apiSend(c, "PUT", fmt.Sprintf("%s/intent-email-configuration/%s/version/%s/published", emailBuilder, d.ConfigID, d.Version),
		map[string]any{"published": true, "last_modified_date": d.LastModified}); err != nil {
		fail(err)
	}
	fmt.Printf("✅ %s email v%s published. Backup: %s\n", a.Name, d.Version, bk)
}

// publishWorkflow activates the latest workflow version on a non-email channel
// (the dashboard's activate call with a version: {version, workflow_types}).
func publishWorkflow(c creds, a agent, o opts) {
	ch := o.channel()
	types, wf, err := channelWorkflow(a, ch)
	if err != nil {
		fail(err)
	}
	b, err := apiGet(c, fmt.Sprintf("/dashboard-controls/solve/v2/workflow-builder/%s/versions/latest", wf))
	if err != nil {
		fail(err)
	}
	latest := field(getMap(decodeJSON(b)), "version", "version_number")
	if latest == "" {
		fail(fmt.Errorf("could not read latest version for %s", a.Name))
	}
	fmt.Printf("Publish %s v%s on %s (workflow %s, types %v)\n", a.Name, latest, ch, wf, types)
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNot published. Re-run with --confirm once approved for this agent.")
		return
	}
	bk := backup("prepublish_"+ch, a.Name, a.Raw)
	n, _ := strconv.Atoi(latest)
	if _, err := apiSend(c, "PUT", fmt.Sprintf("%s/workflow/%s/activate", wfBuilder, wf), map[string]any{"version": n, "workflow_types": types}); err != nil {
		fail(err)
	}
	fmt.Printf("✅ published. Backup: %s\n", bk)
}

// ---------- channel on/off (gated) ----------

const wfBuilder = "/dashboard-controls/solve/v2/workflow-builder"

// channelTypes is the dashboard's channel → workflow_types map (its bundle,
// 2026-10-21). Note the widget is "flamethrower" internally.
var channelTypes = map[string][]string{
	"widget": {"flamethrower-preview", "flamethrower-live"}, "slack": {"slack-live", "slack-preview"},
	"api": {"api-live", "api-preview"}, "voice": {"voice-live", "voice-preview"},
	"web_call": {"web-call-live", "web-call-preview"}, "playbook": {"playbook-live", "playbook-preview"},
	"interactive_email": {"interactive-email-live", "interactive-email-preview"},
}

// channelWFField is where each channel's workflow id lives on the agents-list
// row; like the dashboard, a missing one falls back to intent_workflow_id.
var channelWFField = map[string]string{
	"api": "api_workflow_id", "playbook": "playbook_workflow_id", "slack": "slack_workflow_id",
	"voice": "voice_workflow_id", "web_call": "web_call_workflow_id", "widget": "widget_workflow_id",
	"interactive_email": "interactive_email_workflow_id",
}

func channelWorkflow(a agent, ch string) ([]string, string, error) {
	ch = strings.ReplaceAll(strings.ToLower(ch), "-", "_")
	if ch == "webcall" {
		ch = "web_call"
	}
	types, ok := channelTypes[ch]
	if !ok {
		var names []string
		for k := range channelTypes {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, "", fmt.Errorf("unknown channel %q (email, %s)", ch, strings.Join(names, ", "))
	}
	wf := field(a.Raw, channelWFField[ch])
	if wf == "" {
		wf = field(a.Raw, "intent_workflow_id")
	}
	if wf == "" {
		return nil, "", fmt.Errorf("no %s workflow id for %s", ch, a.Name)
	}
	return types, wf, nil
}

func isLiveOn(a agent, types []string) bool {
	for _, v := range getArr(a.Raw, "active_workflow_types") {
		for _, t := range types {
			if fmt.Sprint(v) == t && strings.HasSuffix(t, "-live") {
				return true
			}
		}
	}
	return false
}

func cmdChannel(c creds, state string, o opts) {
	on := state == "on"
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	ch := o.channel()
	if ch == "email" {
		vs, err := emailVersions(c, a)
		if err != nil {
			fail(err)
		}
		v, err := pickVersion(vs, !o.has("draft"))
		if err != nil {
			fail(err)
		}
		where := "LIVE (published version)"
		if o.has("draft") {
			where = "draft only"
		}
		fmt.Printf("%s email autopilot: %v → %v on v%s — %s\n", a.Name, v.Autopilot, on, v.Version, where)
		if !o.has("confirm") {
			fmt.Fprintln(os.Stderr, "\nNo change made. Re-run with --confirm once approved for this agent.")
			return
		}
		bk := backup("channel_email", a.Name, v.Raw)
		if _, err := apiSend(c, "PUT", fmt.Sprintf("%s/intent-email-configuration/%s/version/%s/autopilot-enabled", emailBuilder, v.ConfigID, v.Version),
			map[string]any{"autopilot_enabled": on, "last_modified_date": v.LastModified}); err != nil {
			fail(err)
		}
		fmt.Printf("✅ %s email is now %s. Backup: %s\n", a.Name, strings.ToUpper(state), bk)
		return
	}
	// other channels: what the dashboard's toggle does, PUT …/workflow/{id}/activate
	// or /deactivate with that channel's workflow_types (a delta, not the full list)
	types, wf, err := channelWorkflow(a, ch)
	if err != nil {
		fail(err)
	}
	now := isLiveOn(a, types)
	fmt.Printf("%s %s: %v → %v (workflow %s, types %v)\n", a.Name, ch, now, on, wf, types)
	if now == on {
		fmt.Println("already in that state; nothing to do")
		return
	}
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNo change made. Re-run with --confirm once approved for this agent.")
		return
	}
	bk := backup("channel_"+ch, a.Name, a.Raw)
	verb := "deactivate"
	if on {
		verb = "activate"
	}
	if _, err := apiSend(c, "PUT", fmt.Sprintf("%s/workflow/%s/%s", wfBuilder, wf, verb), map[string]any{"workflow_types": types}); err != nil {
		fail(err)
	}
	fmt.Printf("✅ %s %s is now %s. Backup: %s\n", a.Name, ch, strings.ToUpper(state), bk)
}
