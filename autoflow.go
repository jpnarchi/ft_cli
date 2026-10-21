// Email autoflow ("autopilot") model, from the dashboard bundle (2026-10-21):
//
//   email config versions : GET  /solve/email/email-builder/intent_definition_id/{id}/email-configurations
//   draft version         : GET  /solve/email/email-builder/intent-email-configuration/{cfg}/draft
//   autopilot workflow    : GET  /solve/autopilot/email-configuration/{cfg}/version/{v}/autopilot-workflow
//   policy                : PUT  /solve/autopilot/autopilot-workflow/{apw}/policy-description   {policy_description}
//   response limit        : PUT  /solve/autopilot/autopilot-workflow/{apw}/max-responses        {max_responses}
//   fallback triggers     : PUT  /solve/autopilot/autopilot-workflow/{apw}/bypass-fallback-triggers {bypass_fallback_triggers}
//   tool (search) config  : PUT  /solve/autopilot/autopilot-workflow/{apw}/tools/{tool}/configuration-fields {configuration_fields}
//   outline               : GET  /solve/autopilot/autopilot-workflow/{apw}/flow-diagram
//   name a version        : PUT  /solve/email/email-builder/intent-email-configuration/{cfg}/version/{v}/name {name}
//   publish               : PUT  .../intent-email-configuration/{cfg}/version/{v}/published     {published, last_modified_date}
//   autopilot on/off      : PUT  .../intent-email-configuration/{cfg}/version/{v}/autopilot-enabled {autopilot_enabled, last_modified_date}
//   journey               : POST /solve/email/email-builder/email-workflow/default {intent_definition_id}  (get-or-create, as the UI does)
//   "/" menu              : GET  /dashboard-controls/solve/v2/workflow-builder/{product}/mention-fields
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	emailBuilder = "/solve/email/email-builder"
	autopilot    = "/solve/autopilot"
)

type emailVersion struct {
	ConfigID, Version, LastModified, By, Name string
	Published, Draft, Autopilot              bool
	Raw                                      map[string]any
}

func toVersion(m map[string]any) (emailVersion, bool) {
	id := field(m, "configuration_id", "intent_email_configuration_id", "email_configuration_id")
	ver := field(m, "version", "configuration_version")
	if id == "" || ver == "" {
		return emailVersion{}, false
	}
	if f, err := strconv.ParseFloat(ver, 64); err == nil {
		ver = strconv.Itoa(int(f))
	}
	return emailVersion{
		ConfigID:     id,
		Version:      ver,
		LastModified: field(m, "last_modified_date", "last_modified", "updated_at"),
		By:           field(m, "last_modified_by", "modified_by", "updated_by", "last_modified_by_email", "created_by"),
		Name:         field(m, "version_name", "name"),
		// the email-configurations list marks the live version with is_in_use; /draft has is_latest_live_version
		Published: field(m, "is_published", "published") == "true" || strings.EqualFold(field(m, "status"), "published") ||
			field(m, "is_in_use") == "true" || field(m, "is_latest_live_version") == "true",
		Draft:        field(m, "is_draft") == "true" || strings.EqualFold(field(m, "status"), "draft"),
		Autopilot:    field(m, "autopilot_enabled", "is_autopilot_enabled") == "true",
		Raw:          m,
	}, true
}

func collectVersions(v any, out *[]emailVersion, seen map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		if ev, ok := toVersion(t); ok && !seen[ev.ConfigID+"/"+ev.Version] {
			seen[ev.ConfigID+"/"+ev.Version] = true
			*out = append(*out, ev)
		}
		for _, x := range t {
			collectVersions(x, out, seen)
		}
	case []any:
		for _, x := range t {
			collectVersions(x, out, seen)
		}
	}
}

func emailVersions(c creds, a agent) ([]emailVersion, error) {
	b, err := apiGet(c, fmt.Sprintf("%s/intent_definition_id/%s/email-configurations", emailBuilder, a.ID))
	if err != nil {
		return nil, err
	}
	var vs []emailVersion
	collectVersions(decodeJSON(b), &vs, map[string]bool{})
	if len(vs) == 0 {
		return nil, fmt.Errorf("%s has no email configuration (not an email autoflow agent?)", a.Name)
	}
	sort.Slice(vs, func(i, j int) bool { return verNum(vs[i]) < verNum(vs[j]) })
	return vs, nil
}

func verNum(v emailVersion) int { n, _ := strconv.Atoi(v.Version); return n }

// pickVersion chooses the published version or the draft (newest unpublished).
func pickVersion(vs []emailVersion, published bool) (emailVersion, error) {
	var best *emailVersion
	for i := range vs {
		v := vs[i]
		if published && v.Published || !published && (v.Draft || !v.Published) {
			if best == nil || verNum(v) > verNum(*best) {
				best = &vs[i]
			}
		}
	}
	if best == nil {
		if published {
			return emailVersion{}, fmt.Errorf("no published version")
		}
		return emailVersion{}, fmt.Errorf("no draft version; open the agent in the UI (or run a write) to start a draft")
	}
	return *best, nil
}

// writableDraft returns the draft version for writes, asking FT for it (the UI
// does the same), and refuses if what comes back is the published version.
func writableDraft(c creds, a agent) (emailVersion, error) {
	vs, err := emailVersions(c, a)
	if err != nil {
		return emailVersion{}, err
	}
	cfg := vs[len(vs)-1].ConfigID
	if b, err := apiGet(c, fmt.Sprintf("%s/intent-email-configuration/%s/draft", emailBuilder, cfg)); err == nil {
		var dv []emailVersion
		collectVersions(decodeJSON(b), &dv, map[string]bool{})
		for _, v := range dv {
			if v.Published {
				continue
			}
			// The draft often has the same number as the live version. Writing to
			// it is what the UI's autosave does: checked 2026-10-21 on a live agent,
			// an autosave edited the v14 autopilot workflow, created no v15,
			// and set needs_to_be_published; contributors only see it after Publish.
			for _, p := range vs {
				if p.Published && p.ConfigID == v.ConfigID && p.Version == v.Version {
					fmt.Fprintf(os.Stderr, "(writes go to v%s's working copy, like the UI's autosave; live changes only on publish)\n", v.Version)
				}
			}
			return v, nil
		}
	}
	d, err := pickVersion(vs, false)
	if err != nil {
		return emailVersion{}, err
	}
	if d.Published {
		return emailVersion{}, fmt.Errorf("refusing to write: the only version of %s is published (live). Start a draft in the UI first", a.Name)
	}
	return d, nil
}

func selectVersion(c creds, a agent, o opts) (emailVersion, error) {
	vs, err := emailVersions(c, a)
	if err != nil {
		return emailVersion{}, err
	}
	if v := o.val("version"); v != "" {
		for _, x := range vs {
			if x.Version == v {
				return x, nil
			}
		}
		return emailVersion{}, fmt.Errorf("version %s not found", v)
	}
	if o.has("published") {
		return pickVersion(vs, true)
	}
	d, err := pickVersion(vs, false)
	if err != nil && !o.has("draft") {
		fmt.Fprintln(os.Stderr, "(no separate draft; showing the live version)")
		return pickVersion(vs, true)
	}
	return d, err
}

func getAutopilot(c creds, v emailVersion) (map[string]any, error) {
	b, err := apiGet(c, fmt.Sprintf("%s/email-configuration/%s/version/%s/autopilot-workflow", autopilot, v.ConfigID, v.Version))
	if err != nil {
		return nil, err
	}
	m, _ := decodeJSON(b).(map[string]any)
	if inner := getMap(m, "autopilot_workflow"); inner != nil {
		m = inner
	}
	if m == nil {
		return nil, fmt.Errorf("unexpected autopilot workflow response")
	}
	return m, nil
}

func apwID(ap map[string]any) string { return field(ap, "autopilot_workflow_id", "id") }

func apwPut(c creds, ap map[string]any, suffix string, body any) error {
	_, err := apiSend(c, "PUT", fmt.Sprintf("%s/autopilot-workflow/%s/%s", autopilot, apwID(ap), suffix), body)
	return err
}

// --- the "/" menu (mention fields) ---

func mentionTargets(c creds) []mentionTarget {
	var out []mentionTarget
	for _, product := range []string{"interactive_email", "email", "workflow_builder"} {
		b, err := apiGet(c, fmt.Sprintf("/dashboard-controls/solve/v2/workflow-builder/%s/mention-fields", product))
		if err != nil {
			continue
		}
		walkMentions(decodeJSON(b), "", &out)
		if len(out) > 0 {
			break
		}
	}
	// context variables and agents are always valid targets too
	if b, err := apiGet(c, epContextVars); err == nil {
		walkMentions(decodeJSON(b), "context_variables", &out)
	}
	if agents, err := listAgents(c); err == nil {
		for _, a := range agents {
			out = append(out, mentionTarget{Kind: "agent", Label: a.Name, ID: a.ID, Raw: a.Raw})
		}
	}
	return out
}

func groupKind(hint string) string {
	h := strings.ToLower(hint)
	switch {
	case strings.Contains(h, "form"):
		return "form"
	case strings.Contains(h, "context") || strings.Contains(h, "variable") || strings.Contains(h, "cv"):
		return "var"
	case strings.Contains(h, "action") || strings.Contains(h, "tool"):
		return "action"
	case strings.Contains(h, "reroute") || strings.Contains(h, "handoff"):
		return "reroute"
	case strings.Contains(h, "intent") || strings.Contains(h, "agent") || strings.Contains(h, "workflow"):
		return "agent"
	}
	return h
}

func walkMentions(v any, hint string, out *[]mentionTarget) {
	switch t := v.(type) {
	case map[string]any:
		label := field(t, "display_name", "context_variable_name", "action_name", "intent_name", "form_name", "name", "label", "title")
		id := field(t, "context_variable_id", "action_id", "intent_definition_id", "intent_workflow_id", "form_id", "id", "value")
		if label != "" && id != "" {
			kind := groupKind(hint)
			if tk := field(t, "type", "mention_type", "category", "field_type"); tk != "" {
				if k := groupKind(tk); k == "var" || k == "action" || k == "agent" || k == "form" || k == "reroute" {
					kind = k
				}
			}
			*out = append(*out, mentionTarget{Kind: kind, Label: label, ID: id, Raw: t})
		}
		for k, x := range t {
			walkMentions(x, k, out)
		}
	case []any:
		for _, x := range t {
			walkMentions(x, hint, out)
		}
	}
}

func cmdChipsList(c creds, o opts, only string) {
	ts := mentionTargets(c)
	sort.Slice(ts, func(i, j int) bool { return ts[i].Kind+ts[i].Label < ts[j].Kind+ts[j].Label })
	seen := map[string]bool{}
	n := 0
	for _, t := range ts {
		if only != "" && t.Kind != only {
			continue
		}
		k := t.Kind + "|" + t.Label
		if seen[k] {
			continue
		}
		seen[k] = true
		fmt.Printf("{{%s: %s}}\t%s\n", t.Kind, t.Label, t.ID)
		n++
	}
	fmt.Fprintf(os.Stderr, "\n%d entries\n", n)
}

// --- agent get ---

func intentInfo(c creds, a agent) (desc string, phrases []string, raw map[string]any) {
	b, err := apiGet(c, intentPath(a.ID))
	if err != nil {
		return "", nil, nil
	}
	m, _ := decodeJSON(b).(map[string]any)
	if inner := getMap(m, "intent"); inner != nil {
		m = inner
	}
	desc = field(m, "description", "intent_description")
	for _, p := range getArr(m, "phrases", "training_phrases", "intent_phrases") {
		switch x := p.(type) {
		case string:
			phrases = append(phrases, x)
		case map[string]any:
			phrases = append(phrases, field(x, "phrase_text", "phrase", "text"))
		}
	}
	return desc, phrases, m
}

func cmdAgentGet(c creds, o opts) {
	a, err := resolveAgent(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	if o.channel() != "email" {
		agentGetGeneric(c, a, o)
		return
	}
	v, err := selectVersion(c, a, o)
	if err != nil {
		fail(err)
	}
	ap, err := getAutopilot(c, v)
	if err != nil {
		fail(err)
	}
	if o.has("raw", "json") {
		r, _ := redact(map[string]any{"version": v.Raw, "autopilot_workflow": ap})
		fmt.Println(jsonText(r))
		return
	}
	text, cm := policyToText(field(ap, "policy_description"))
	if out := o.val("out", "o"); out != "" {
		os.WriteFile(out, []byte(text), 0o644)
		os.WriteFile(out+".chips.json", []byte(jsonText(cm)), 0o600)
		fmt.Fprintf(os.Stderr, "wrote %s (+ %s.chips.json)\n", out, out)
	}
	desc, phrases, _ := intentInfo(c, a)
	state := "draft"
	if v.Published {
		state = "published"
	}
	fmt.Printf("# %s — email, v%s (%s)", a.Name, v.Version, state)
	if v.Name != "" {
		fmt.Printf(" “%s”", v.Name)
	}
	fmt.Printf("\nlast edited: %s %s   autopilot: %v\n\n", v.LastModified, v.By, v.Autopilot)
	fmt.Printf("## Intent description\n%s\n\n", strings.TrimSpace(desc))
	if len(phrases) > 0 {
		fmt.Printf("## Training phrases (%d)\n", len(phrases))
		for _, p := range phrases {
			fmt.Printf("* %s\n", p)
		}
		fmt.Println()
	}
	fmt.Printf("## Policy\n%s\n", text)
	fmt.Printf("## Settings\nresponse_limit (max_responses): %s\nbypass_fallback_triggers: %s\n\n",
		orDash(field(ap, "max_responses")), orDash(field(ap, "bypass_fallback_triggers")))
	if tools := getArr(ap, "tools", "tool_definitions"); len(tools) > 0 {
		fmt.Println("## Tools / actions")
		for _, t := range tools {
			if m, ok := t.(map[string]any); ok {
				fmt.Printf("* %s  [%s]\n", field(m, "tool_name", "name", "action_name", "display_name"), field(m, "tool_id", "id"))
			}
		}
		fmt.Println()
	}
	if cvs := getArr(ap, "context_variables"); len(cvs) > 0 {
		var names []string
		for _, x := range cvs {
			if m, ok := x.(map[string]any); ok {
				names = append(names, field(m, "display_name", "context_variable_name", "name"))
			} else {
				names = append(names, fmt.Sprint(x))
			}
		}
		fmt.Printf("## Context variables\n%s\n\n", strings.Join(names, ", "))
	}
	if b, err := apiGet(c, fmt.Sprintf("%s/autopilot-workflow/%s/flow-diagram", autopilot, apwID(ap))); err == nil {
		fmt.Printf("## Outline\n%s\n\n", outlineText(decodeJSON(b)))
	}
	printJourney(c, a)
}

func orDash(s string) string {
	if s == "" || s == "<nil>" {
		return "—"
	}
	return s
}

// outlineText renders the flow diagram's node labels as an indented list.
func outlineText(v any) string {
	var lines []string
	var walk func(any, int)
	walk = func(x any, d int) {
		switch t := x.(type) {
		case map[string]any:
			if l := field(t, "label", "title", "text", "description"); l != "" && len(l) < 300 {
				lines = append(lines, strings.Repeat("  ", d)+"* "+strings.ReplaceAll(l, "\n", " "))
				d++
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k], d)
			}
		case []any:
			for _, y := range t {
				walk(y, d)
			}
		}
	}
	walk(v, 0)
	if len(lines) == 0 {
		return "—"
	}
	if len(lines) > 60 {
		lines = append(lines[:60], "  …")
	}
	return strings.Join(lines, "\n")
}

// printJourney shows the email Journey steps that set tags/fields/status.
func printJourney(c creds, a agent) {
	b, err := apiSend(c, "POST", emailBuilder+"/email-workflow/default", map[string]any{"intent_definition_id": a.ID})
	if err != nil {
		return
	}
	var steps []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if st := field(t, "step_type"); st != "" {
				sf, _ := json.Marshal(t["step_fields"])
				s := string(sf)
				if len(s) > 220 {
					s = s[:220] + "…"
				}
				steps = append(steps, fmt.Sprintf("* %s %s", st, s))
			}
			for _, y := range t {
				walk(y)
			}
		case []any:
			for _, y := range t {
				walk(y)
			}
		}
	}
	walk(decodeJSON(b))
	if len(steps) > 0 {
		fmt.Printf("## Journey (tags / fields / status)\n%s\n", strings.Join(steps, "\n"))
	}
}

// agentGetGeneric covers non-email channels: intent + workflow, any policy HTML
// fields rendered with chips.
func agentGetGeneric(c creds, a agent, o opts) {
	desc, phrases, _ := intentInfo(c, a)
	b, err := apiGet(c, flowsPath(a.ID))
	if err != nil {
		fail(err)
	}
	if o.has("raw", "json") {
		fmt.Println(prettyJSON(b))
		return
	}
	fmt.Printf("# %s — %s\nlive on: %s\n\n## Intent description\n%s\n\n", a.Name, o.channel(), strings.Join(liveChannels(a), ","), desc)
	for _, p := range phrases {
		fmt.Printf("* %s\n", p)
	}
	var walk func(any, string)
	walk = func(x any, k string) {
		switch t := x.(type) {
		case map[string]any:
			for kk, y := range t {
				walk(y, kk)
			}
		case []any:
			for _, y := range t {
				walk(y, k)
			}
		case string:
			if strings.Contains(strings.ToLower(k), "policy") && strings.Contains(t, "<") {
				txt, _ := policyToText(t)
				fmt.Printf("\n## Policy (%s)\n%s\n", k, txt)
			}
		}
	}
	walk(decodeJSON(b), "")
}
