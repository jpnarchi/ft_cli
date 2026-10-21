// Agent listing and resolution helpers.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// agent is the subset of an intents-list entry we use. Extra fields are kept in
// Raw for commands that need them.
type agent struct {
	ID       string
	Name     string
	Type     string
	IsAuto   bool
	Workflow string
	Raw      map[string]any
}

func field(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
	}
	return ""
}

func parseAgents(b []byte) ([]agent, error) {
	var root struct {
		Intents []map[string]any `json:"intents"`
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	var out []agent
	for _, m := range root.Intents {
		a := agent{
			ID:       field(m, "intent_definition_id"),
			Name:     field(m, "intent_name"),
			Workflow: field(m, "intent_workflow_id", "api_workflow_id"),
			Raw:      m,
		}
		a.IsAuto = field(m, "is_autoflow") == "true"
		if a.IsAuto {
			a.Type = "autoflow"
		} else {
			a.Type = "classic"
		}
		out = append(out, a)
	}
	return out, nil
}

func listAgents(c creds) ([]agent, error) {
	b, err := apiGet(c, epIntents)
	if err != nil {
		return nil, err
	}
	return parseAgents(b)
}

// resolveAgent matches by exact id, exact name, or unique case-insensitive
// substring of the name.
func resolveAgent(c creds, q string) (agent, error) {
	agents, err := listAgents(c)
	if err != nil {
		return agent{}, err
	}
	ql := strings.ToLower(strings.TrimSpace(q))
	var hits []agent
	for _, a := range agents {
		if a.ID == q || strings.ToLower(a.Name) == ql {
			return a, nil
		}
		if strings.Contains(strings.ToLower(a.Name), ql) {
			hits = append(hits, a)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return agent{}, fmt.Errorf("no agent matches %q", q)
	default:
		var names []string
		for _, h := range hits {
			names = append(names, h.Name)
		}
		sort.Strings(names)
		return agent{}, fmt.Errorf("%q matches several: %s", q, strings.Join(names, ", "))
	}
}

func cmdAgents(c creds, args []string) {
	channel := ""
	activeOnly := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--channel", "-c":
			if i+1 < len(args) {
				channel = strings.ToLower(args[i+1])
				i++
			}
		case "--active", "-a":
			activeOnly = true
		}
	}
	agents, err := listAgents(c)
	if err != nil {
		fail(err)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	fmt.Printf("%-38s  %-9s  %-22s  %s\n", "NAME", "TYPE", "LIVE ON", "ID")
	n := 0
	for _, a := range agents {
		live := liveChannels(a)
		// channel filter: capability unless --active, then must be live there
		if channel != "" {
			if activeOnly {
				if !contains(live, channel) {
					continue
				}
			} else if !contains(agentChannels(a), channel) {
				continue
			}
		} else if activeOnly && len(live) == 0 {
			continue
		}
		liveStr := strings.Join(live, ",")
		if liveStr == "" {
			liveStr = "—"
		}
		fmt.Printf("%-38s  %-9s  %-22s  %s\n", trunc(a.Name, 38), a.Type, trunc(liveStr, 22), a.ID)
		n++
	}
	label := "agents"
	if activeOnly {
		label = "active agents"
	}
	if channel != "" {
		fmt.Printf("\n%d %s on channel %q\n", n, label, channel)
	} else {
		fmt.Printf("\n%d %s\n", n, label)
	}
}

// agentChannels reads the intent's channels array (capability).
func agentChannels(a agent) []string {
	raw, _ := a.Raw["channels"].([]any)
	var out []string
	for _, v := range raw {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// liveChannels returns the channels an agent is actually ACTIVE on, from
// active_workflow_types (entries like "slack-live") plus email autopilot.
func liveChannels(a agent) []string {
	seen := map[string]bool{}
	raw, _ := a.Raw["active_workflow_types"].([]any)
	for _, v := range raw {
		s := fmt.Sprint(v)
		if strings.HasSuffix(s, "-live") {
			seen[strings.TrimSuffix(s, "-live")] = true
		}
	}
	if field(a.Raw, "email_autopilot_enabled") == "true" {
		seen["email"] = true
	}
	var out []string
	for ch := range seen {
		out = append(out, ch)
	}
	sort.Strings(out)
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
