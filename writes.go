// Draft writes (diff first; --apply writes; backup before every write) and the
// gated live commands (publish, channel on/off need --confirm).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	newHTML, err := textToPolicy(string(newText), cm, targets)
	if err != nil {
		fail(err)
	}
	rendered, _ := policyToText(newHTML)
	fmt.Printf("%s — email draft v%s\n\n%s\n", a.Name, v.Version, lineDiff(curText, rendered))
	if rendered == curText {
		return
	}
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
	for _, ch := range changes {
		fmt.Printf("  %s: %s → %s\n", ch.key, ch.from, ch.to)
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
		cf := t["configuration_fields"]
		js := jsonText(cf)
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
	fmt.Printf("%s — draft v%s, tool %s\n\n%s\n", a.Name, v.Version, field(tool, "tool_id", "id"), lineDiff(jsonText(tool["configuration_fields"]), jsonText(newCF)))
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
	before := fmt.Sprintf("name: %s\ndescription:\n%s\nphrases:\n%s", a.Name, desc, strings.Join(phrases, "\n"))
	after := fmt.Sprintf("name: %s\ndescription:\n%s\nphrases:\n%s", newName, newDesc, strings.Join(newPhrases, "\n"))
	fmt.Println(lineDiff(before, after))
	if before == after {
		return
	}
	if !o.has("apply") {
		dryRunNote("set-intent")
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
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, safeName(name)+".json")
	os.WriteFile(p, []byte(jsonText(map[string]any{"saved_at": time.Now().Format(time.RFC3339), "version": v.Raw, "autopilot_workflow": ap})), 0o600)
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
		} else if jsonText(cur["configuration_fields"]) != jsonText(m["configuration_fields"]) {
			fmt.Printf("  tool %s configuration will be restored\n", field(m, "tool_name", "name"))
			toolUpdates = append(toolUpdates, m)
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
	apwPut(c, dst, "max-responses", map[string]any{"max_responses": mr})
	apwPut(c, dst, "bypass-fallback-triggers", map[string]any{"bypass_fallback_triggers": field(src, "bypass_fallback_triggers") == "true"})
	for _, t := range toolUpdates {
		apwPut(c, dst, "tools/"+field(t, "tool_id", "id")+"/configuration-fields", map[string]any{"configuration_fields": t["configuration_fields"]})
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
	if p, err := pickVersion(vs, true); err == nil {
		if pubAP, err = getAutopilot(c, p); err == nil {
			pubText, _ = policyToText(field(pubAP, "policy_description"))
		}
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

// publishWorkflow activates the latest workflow version on a non-email channel.
func publishWorkflow(c creds, a agent, o opts) {
	ch := o.channel()
	b, err := apiGet(c, fmt.Sprintf("/dashboard-controls/solve/v2/workflow-builder/%s/versions/latest", a.Workflow))
	if err != nil {
		fail(err)
	}
	latest := field(getMap(decodeJSON(b)), "version", "version_number")
	if latest == "" {
		fail(fmt.Errorf("could not read latest version for %s", a.Name))
	}
	types := withType(activeTypes(a), ch+"-live", true)
	fmt.Printf("Publish %s v%s on %s → workflow_types %v\n", a.Name, latest, ch, types)
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNot published. Re-run with --confirm once approved for this agent.")
		return
	}
	bk := backup("prepublish_"+ch, a.Name, a.Raw)
	activate(c, a, latest, types)
	fmt.Printf("✅ published. Backup: %s\n", bk)
}

// ---------- channel on/off (gated) ----------

func activeTypes(a agent) []string {
	var out []string
	for _, v := range getArr(a.Raw, "active_workflow_types") {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func withType(ts []string, t string, on bool) []string {
	var out []string
	for _, x := range ts {
		if x != t {
			out = append(out, x)
		}
	}
	if on {
		out = append(out, t)
	}
	return out
}

func activate(c creds, a agent, version string, types []string) {
	n, _ := strconv.Atoi(version)
	if _, err := apiSend(c, "PUT", fmt.Sprintf("/dashboard-controls/solve/v2/workflow-builder/workflow/%s/activate", a.Workflow),
		map[string]any{"version": n, "workflow_types": types}); err != nil {
		fail(err)
	}
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
	version := o.val("version")
	if version == "" {
		version = field(a.Raw, "active_version", "published_version", "workflow_version")
	}
	if version == "" {
		fail(fmt.Errorf("pass --version N (the version that should stay live; see ft agent versions %q --channel %s)", a.Name, ch))
	}
	types := withType(activeTypes(a), ch+"-live", on)
	fmt.Printf("%s %s: workflow_types %v → %v (v%s)\n", a.Name, ch, activeTypes(a), types, version)
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNo change made. Re-run with --confirm once approved for this agent.")
		return
	}
	bk := backup("channel_"+ch, a.Name, a.Raw)
	activate(c, a, version, types)
	fmt.Printf("✅ done. Backup: %s\n", bk)
}
