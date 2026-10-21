// Coaching (AI instructions): list, get, update the draft, publish, discard.
//
// Endpoints and bodies are from the dashboard bundle (2026-10-21): the UI's
// updateInstruction POSTs the whole instruction object with the changed fields
// to /ai-instruction/{id}, then calls /ai-instruction/{id}/publish.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

const coachingBase = "/dashboard-controls/solve/v2"

type coachingItem struct {
	ID, Name, Instructions string
	Active, Draft, Unsaved bool
	Version                string
	Raw                    map[string]any
}

func toCoaching(m map[string]any) coachingItem {
	return coachingItem{
		ID:           field(m, "ai_instruction_id"),
		Name:         field(m, "name"),
		Instructions: field(m, "instructions"),
		Active:       field(m, "is_active") == "true",
		Draft:        field(m, "is_draft") == "true",
		Unsaved:      field(m, "unsaved_changes") == "true",
		Version:      field(m, "version"),
		Raw:          m,
	}
}

func listCoaching(c creds, mode string) ([]coachingItem, error) {
	b, err := apiGet(c, coachingBase+"/ai-instructions?draft_mode="+mode)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("unexpected ai-instructions response: %w", err)
	}
	var out []coachingItem
	for _, m := range rows {
		out = append(out, toCoaching(m))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func getCoaching(c creds, id string) (coachingItem, error) {
	b, err := apiGet(c, coachingBase+"/ai-instruction/"+id)
	if err != nil {
		return coachingItem{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return coachingItem{}, err
	}
	return toCoaching(m), nil
}

// resolveCoaching finds an instruction by id, exact name, or unique substring,
// searching the draft list (which includes unpublished instructions).
func resolveCoaching(c creds, q string) (coachingItem, error) {
	if q == "" {
		return coachingItem{}, fmt.Errorf("name or id required (see ft coaching list)")
	}
	items, err := listCoaching(c, "draft")
	if err != nil {
		return coachingItem{}, err
	}
	var subs, exact []coachingItem
	for _, it := range items {
		if it.ID == q {
			return getCoaching(c, it.ID)
		}
		if strings.EqualFold(it.Name, q) {
			exact = append(exact, it)
		}
		if strings.Contains(strings.ToLower(it.Name), strings.ToLower(q)) {
			subs = append(subs, it)
		}
	}
	if len(exact) == 1 {
		return getCoaching(c, exact[0].ID)
	}
	if len(exact) > 1 {
		var ids []string
		for _, e := range exact {
			ids = append(ids, e.ID)
		}
		return coachingItem{}, fmt.Errorf("%d instructions are named %q; use the id: %s", len(exact), q, strings.Join(ids, ", "))
	}
	switch len(subs) {
	case 1:
		return getCoaching(c, subs[0].ID)
	case 0:
		return coachingItem{}, fmt.Errorf("no coaching instruction matches %q", q)
	}
	var names []string
	for _, s := range subs {
		names = append(names, s.Name)
	}
	return coachingItem{}, fmt.Errorf("%q matches %d instructions: %s", q, len(subs), strings.Join(names, "; "))
}

// coachingScope names the agent an instruction is attached to, or "global".
func coachingScope(m map[string]any, byWF map[string]string) string {
	for _, k := range []string{"autoflow_intent_workflow_id", "intent_workflow_id"} {
		if wf := field(m, k); wf != "" {
			if n, ok := byWF[wf]; ok {
				return n
			}
			return "workflow " + trunc(wf, 12)
		}
	}
	if field(m, "intent_definition_id") != "" || field(m, "email_component_id") != "" {
		return "email step"
	}
	return "global"
}

// agentWorkflowNames maps every workflow id an agent owns (intent workflow,
// per-channel workflows, email autopilot workflow) to the agent's name, which
// is how coaching rows point at agents. One agent's calls failing just leaves
// its ids unnamed.
func agentWorkflowNames(c creds) map[string]string {
	out := map[string]string{}
	as, err := listAgents(c)
	if err != nil {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, a := range as {
		wg.Add(1)
		go func(a agent) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ids := []string{a.Workflow}
			for _, p := range []string{intentPath(a.ID), flowsPath(a.ID)} {
				if b, err := apiGet(c, p); err == nil {
					collectWorkflowIDs(decodeJSON(b), "", &ids)
				}
			}
			if a.IsAuto {
				if vs, err := emailVersions(c, a); err == nil {
					for _, v := range vs {
						if v.Published || v.Draft {
							if ap, err := getAutopilot(c, v); err == nil {
								ids = append(ids, apwID(ap))
							}
						}
					}
				}
			}
			mu.Lock()
			for _, id := range ids {
				if id != "" {
					out[id] = a.Name
				}
			}
			mu.Unlock()
		}(a)
	}
	wg.Wait()
	return out
}

func collectWorkflowIDs(v any, key string, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			collectWorkflowIDs(e, k, out)
		}
	case []any:
		for _, e := range x {
			collectWorkflowIDs(e, key, out)
		}
	case string:
		if strings.HasSuffix(key, "workflow_id") {
			*out = append(*out, x)
		}
	}
}

func cmdCoachingList(c creds, o opts) {
	mode := "published"
	if o.has("draft") {
		mode = "draft"
	}
	items, err := listCoaching(c, mode)
	if err != nil {
		fail(err)
	}
	byWF := agentWorkflowNames(c)
	var only *agent
	if q := o.val("agent", "a"); q != "" {
		a, err := resolveAgent(c, q)
		if err != nil {
			fail(err)
		}
		only = &a
	}
	fmt.Printf("%s coaching (%d)\n%-38s %-36s %-7s %-8s %-24s %s\n", mode, len(items), "ID", "NAME", "ACTIVE", "STATE", "SCOPE", "PRODUCTS")
	for _, it := range items {
		scope := coachingScope(it.Raw, byWF)
		if only != nil && scope != only.Name {
			continue
		}
		st := "live"
		if it.Draft {
			st = "draft"
		}
		if it.Unsaved {
			st += "*"
		}
		var prods []string
		for _, p := range getArr(it.Raw, "products") {
			prods = append(prods, fmt.Sprint(p))
		}
		fmt.Printf("%-38s %-36s %-7v %-8s %-24s %s\n", it.ID, trunc(orDash(it.Name), 36), it.Active, st, trunc(scope, 24), strings.Join(prods, ","))
	}
	if mode == "published" {
		fmt.Fprintln(os.Stderr, "\n(--draft shows drafts and unpublished instructions; * = unsaved changes)")
	}
}

func cmdCoachingGet(c creds, o opts) {
	it, err := resolveCoaching(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	if o.has("raw", "json") {
		fmt.Println(jsonText(it.Raw))
		return
	}
	if f := o.val("out", "o"); f != "" {
		if err := os.WriteFile(f, []byte(it.Instructions+"\n"), 0o600); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s (edit it, then: ft coaching update %q --file %s)\n", f, it.Name, f)
		return
	}
	fmt.Printf("# %s\nid: %s   version: %s   active: %v   draft: %v   unsaved changes: %v\n\n%s\n",
		it.Name, it.ID, it.Version, it.Active, it.Draft, it.Unsaved, it.Instructions)
}

func coachingSummary(name, instr string, active bool) string {
	return fmt.Sprintf("name: %s\nactive: %v\ninstructions:\n%s", name, active, strings.TrimRight(instr, "\n"))
}

func cmdCoachingUpdate(c creds, o opts) {
	it, err := resolveCoaching(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	name, instr, active := it.Name, it.Instructions, it.Active
	if v := o.val("name"); v != "" {
		name = v
	}
	if f := o.val("file", "f"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			fail(err)
		}
		instr = strings.TrimRight(string(b), "\n")
	}
	if v := strings.ToLower(o.val("active")); v != "" {
		switch v {
		case "on", "true", "yes":
			active = true
		case "off", "false", "no":
			active = false
		default:
			fail(fmt.Errorf("--active takes on or off"))
		}
	}
	if o.val("name") == "" && o.val("file", "f") == "" && o.val("active") == "" {
		fail(fmt.Errorf(`usage: ft coaching update <name|id> [--file instructions.md] [--name "…"] [--active on|off] [--apply]`))
	}
	before := coachingSummary(it.Name, it.Instructions, it.Active)
	after := coachingSummary(name, instr, active)
	fmt.Printf("%s — coaching draft (v%s)\n\n%s\n", it.Name, it.Version, lineDiff(before, after))
	if before == after {
		return
	}
	if !o.has("apply") {
		dryRunNote("coaching update")
		return
	}
	bk := backup("coaching", it.Name, it.Raw)
	body := map[string]any{}
	for k, v := range it.Raw {
		body[k] = v
	}
	body["name"], body["instructions"], body["is_active"] = name, instr, active
	if _, err := apiSend(c, "POST", coachingBase+"/ai-instruction/"+it.ID, body); err != nil {
		fail(err)
	}
	if got, err := getCoaching(c, it.ID); err == nil {
		if coachingSummary(got.Name, got.Instructions, got.Active) != after {
			fmt.Fprintln(os.Stderr, "⚠️  saved, but what FT stored differs from what was sent; run `ft coaching get` to review")
		}
	}
	fmt.Printf("\n✅ coaching draft updated. Backup: %s\nPublish with: ft coaching publish %q --confirm\n", bk, it.Name)
}

func cmdCoachingPublish(c creds, o opts) {
	it, err := resolveCoaching(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	var pub *coachingItem
	if items, err := listCoaching(c, "published"); err == nil {
		for i := range items {
			if items[i].ID == it.ID {
				pub = &items[i]
			}
		}
	}
	before := "(not published yet)"
	if pub != nil {
		before = coachingSummary(pub.Name, pub.Instructions, pub.Active)
	}
	fmt.Printf("Publish coaching %q → live\n\n## published → draft\n%s\n", it.Name, lineDiff(before, coachingSummary(it.Name, it.Instructions, it.Active)))
	if !o.has("confirm") {
		fmt.Fprintln(os.Stderr, "\nNot published. Coaching applies to live conversations: re-run with --confirm once it's approved.")
		return
	}
	var pubRaw map[string]any
	if pub != nil {
		pubRaw = pub.Raw
	}
	bk := backup("coaching_prepublish", it.Name, map[string]any{"published": pubRaw, "draft": it.Raw})
	if _, err := apiSend(c, "POST", coachingBase+"/ai-instruction/"+it.ID+"/publish", nil); err != nil {
		fail(err)
	}
	fmt.Printf("✅ coaching %q published. Backup: %s\n", it.Name, bk)
}

func cmdCoachingDiscard(c creds, o opts) {
	it, err := resolveCoaching(c, o.agentArg())
	if err != nil {
		fail(err)
	}
	fmt.Printf("Discard the draft of coaching %q (v%s); the published version stays as is.\n", it.Name, it.Version)
	if !o.has("apply") {
		dryRunNote("coaching discard")
		return
	}
	bk := backup("coaching_discarded_draft", it.Name, it.Raw)
	if _, err := apiSend(c, "POST", coachingBase+"/ai-instruction/"+it.ID+"/discard-draft", nil); err != nil {
		fail(err)
	}
	fmt.Printf("✅ draft discarded. Backup of the draft: %s\n", bk)
}
