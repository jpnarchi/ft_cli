// Read an agent's full config: the intent/autoflow config plus the email
// configuration (where the policy text and templates live). `--text` extracts
// just the human-readable policy so chips and nested JSON don't obscure it.
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// fetchAgentConfig returns intent config and email config as a merged object.
func fetchAgentConfig(c creds, a agent) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for label, path := range map[string]string{
		"intent": intentPath(a.ID),
		"email":  emailCfgPath(a.ID),
	} {
		b, err := apiGet(c, path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if !json.Valid(b) {
			b, _ = json.Marshal(string(b))
		}
		out[label] = b
	}
	return out, nil
}

func cmdPolicy(c creds, args []string) {
	textMode := false
	var rest []string
	for _, a := range args {
		if a == "--text" || a == "-t" {
			textMode = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		fail(fmt.Errorf("usage: ft policy [--text] <agent>"))
	}
	a, err := resolveAgent(c, strings.Join(rest, " "))
	if err != nil {
		fail(err)
	}
	cfg, err := fetchAgentConfig(c, a)
	if err != nil {
		fail(err)
	}
	if !textMode {
		merged, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(prettyJSON(merged))
		return
	}
	fmt.Printf("# %s  (%s)\n\n", a.Name, a.ID)
	var fields []textField
	for _, raw := range cfg {
		var v any
		json.Unmarshal(raw, &v)
		walkText(v, "", &fields)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		s := readable(f.Text)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		label := f.Path[strings.LastIndexAny(f.Path, ".]")+1:]
		fmt.Printf("## %s\n%s\n\n", label, s)
	}
}

// --- shared text extraction (used by policy --text and lint) ---

type textField struct{ Path, Text string }

var interestingKey = regexp.MustCompile(`(?i)(name|title|policy|prompt|instruction|template|description|guidance|constraint|tone|context|objective|role|audience|step|rule|message|text|content|label|response|reasoning|trigger|scenario|guardrail)`)
var uuidLike = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var numKey = regexp.MustCompile(`(?i)(limit|max|enabled|is_|count)`)

func walkText(v any, path string, out *[]textField) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkText(t[k], path+"."+k, out)
		}
	case []any:
		for i, x := range t {
			walkText(x, fmt.Sprintf("%s[%d]", path, i), out)
		}
	case string:
		s := strings.TrimSpace(t)
		if s == "" || uuidLike.MatchString(s) {
			return
		}
		last := path[strings.LastIndexAny(path, ".]")+1:]
		if len(s) >= 40 || interestingKey.MatchString(last) {
			*out = append(*out, textField{path, s})
		}
	case float64, bool:
		last := path[strings.LastIndexAny(path, ".]")+1:]
		if numKey.MatchString(last) {
			*out = append(*out, textField{path, fmt.Sprint(t)})
		}
	}
}

var htmlTag = regexp.MustCompile(`<[^>]+>`)
var chipAttr = regexp.MustCompile(`<span[^>]*data-[a-z-]*(?:name|label|value)="([^"]+)"[^>]*>`)

// readable turns stored markup/chips into plain text with {chip} markers.
func readable(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "</p>", "\n", "</li>", "\n", "<li>", "• ").Replace(s)
	s = chipAttr.ReplaceAllString(s, "{$1}")
	return strings.TrimSpace(htmlTag.ReplaceAllString(s, ""))
}
