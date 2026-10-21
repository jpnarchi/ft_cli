// Audit/read commands: actions, version history, stats, snapshots, lint.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func prettyJSON(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	return string(out)
}

func cmdActions(c creds) {
	b, err := apiGet(c, epActions)
	if err != nil {
		fail(err)
	}
	fmt.Println(prettyJSON(b))
}

func cmdVersions(c creds, q string) {
	a, err := resolveAgent(c, q)
	if err != nil {
		fail(err)
	}
	if a.Workflow == "" {
		fail(fmt.Errorf("no workflow id for %s", a.Name))
	}
	b, err := apiGet(c, versionPath(a.Workflow))
	if err != nil {
		fail(err)
	}
	fmt.Println(prettyJSON(b))
}

func cmdStats(c creds, args []string) {
	days := 7
	for i := 0; i < len(args); i++ {
		if args[i] == "--days" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &days)
		}
	}
	to := time.Now().Unix()
	from := time.Now().AddDate(0, 0, -days).Unix()
	b, err := apiGet(c, countsPath(from, to))
	if err != nil {
		fail(err)
	}
	agents, err := parseAgents(b)
	if err != nil {
		fmt.Println(prettyJSON(b))
		return
	}
	type row struct {
		name                         string
		received, responses, deflect float64
	}
	var rows []row
	for _, a := range agents {
		ch, _ := a.Raw["inquiry_count_per_channel"].(map[string]any)
		get := func(k string) float64 { v, _ := ch[k].(float64); return v }
		rows = append(rows, row{a.Name, get("email"), 0, 0})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].received > rows[j].received })
	fmt.Printf("Email inquiries, last %d days:\n\n%-38s  %s\n", days, "AGENT", "EMAILS")
	for _, r := range rows {
		if r.received == 0 {
			continue
		}
		fmt.Printf("%-38s  %.0f\n", trunc(r.name, 38), r.received)
	}
}

func snapshotRoot() string { return filepath.Join(".", "snapshots") }

func cmdSnapshot(c creds) {
	agents, err := listAgents(c)
	if err != nil {
		fail(err)
	}
	dir := filepath.Join(snapshotRoot(), time.Now().Format("20060102-1504"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	ok := 0
	for _, a := range agents {
		cfg, err := fetchAgentConfig(c, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", a.Name, err)
			continue
		}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		os.WriteFile(filepath.Join(dir, safeName(a.Name)+"__"+a.ID+".json"), b, 0o644)
		ok++
	}
	fmt.Printf("Saved %d/%d agents to %s\n", ok, len(agents), dir)
}

func safeName(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", ":", "_", "&", "and").Replace(strings.ToLower(s))
}

// --- lint ---

type lintRule struct {
	name string
	re   *regexp.Regexp
	why  string
}

var lintRules = []lintRule{
	{"support-email", regexp.MustCompile(`(?i)[a-z0-9._%+-]*support[a-z0-9._%+-]*@[a-z0-9.-]+\.[a-z]{2,}`), "leaks a support email address to contributors"},
	{"solved-leak", regexp.MustCompile(`(?i)mark (the )?ticket.{0,8}solved`), "internal 'mark ticket Solved' instruction may leak into a reply"},
	{"github-source", regexp.MustCompile(`(?i)(github\.com|faq-hub|support-kb|raw\.githubusercontent)`), "references a GitHub knowledge source instead of the public Help Center"},
	{"internal-guide", regexp.MustCompile(`(?i)(internal guide|agent guide|playbook|private doc)`), "may quote an internal guide to contributors"},
	{"empty-chip", regexp.MustCompile(`reroute to \.|if\s+is (true|false)|check for this contributor\b`), "an inline chip/variable looks empty in the stored text"},
}

func cmdLint(c creds, q string) {
	var agents []agent
	if q == "" {
		var err error
		if agents, err = listAgents(c); err != nil {
			fail(err)
		}
	} else {
		a, err := resolveAgent(c, q)
		if err != nil {
			fail(err)
		}
		agents = []agent{a}
	}
	total := 0
	for _, a := range agents {
		cfg, err := fetchAgentConfig(c, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", a.Name, err)
			continue
		}
		var fields []textField
		for _, raw := range cfg {
			var v any
			json.Unmarshal(raw, &v)
			walkText(v, "", &fields)
		}
		var found []string
		for _, f := range fields {
			s := readable(f.Text)
			for _, r := range lintRules {
				if r.re.MatchString(s) {
					found = append(found, fmt.Sprintf("  [%s] %s\n    ↳ %s", r.name, r.why, trunc(strings.ReplaceAll(s, "\n", " "), 100)))
				}
			}
		}
		if len(found) > 0 {
			fmt.Printf("## %s\n%s\n\n", a.Name, strings.Join(dedup(found), "\n"))
			total += len(found)
		}
	}
	fmt.Printf("%d finding(s)\n", total)
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
