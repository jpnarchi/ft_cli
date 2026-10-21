// Shared helpers: flag parsing, confirmations, backups, diffs.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// opts is a parsed command line: positionals, --key value flags, bool flags,
// and the --thread message list.
type opts struct {
	pos    []string
	vals   map[string]string
	multi  map[string][]string // every value of a repeated flag, in order
	bools  map[string]bool
	thread []string
}

var boolFlags = map[string]bool{
	"apply": true, "confirm": true, "published": true, "draft": true, "real": true,
	"mock-actions": true, "raw": true, "full": true, "active": true, "fast": true,
	"json": true, "text": true, "t": true, "a": true, "logs": true,
}

func parseOpts(args []string) opts {
	o := opts{vals: map[string]string{}, multi: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			o.pos = append(o.pos, a)
			continue
		}
		k := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(k, '='); eq > 0 {
			o.vals[k[:eq]] = k[eq+1:]
			o.multi[k[:eq]] = append(o.multi[k[:eq]], k[eq+1:])
			continue
		}
		switch {
		case k == "thread":
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				o.thread = append(o.thread, args[i])
			}
		case k == "m": // repeatable single message
			if i+1 < len(args) {
				i++
				o.thread = append(o.thread, args[i])
			}
		case boolFlags[k]:
			o.bools[k] = true
		default:
			if i+1 < len(args) {
				i++
				o.vals[k] = args[i]
				o.multi[k] = append(o.multi[k], args[i])
			} else {
				o.bools[k] = true
			}
		}
	}
	return o
}

func (o opts) val(keys ...string) string {
	for _, k := range keys {
		if v, ok := o.vals[k]; ok {
			return v
		}
	}
	return ""
}

func (o opts) has(keys ...string) bool {
	for _, k := range keys {
		if o.bools[k] {
			return true
		}
	}
	return false
}

func (o opts) channel() string {
	ch := strings.ToLower(o.val("channel", "c"))
	if ch == "" {
		ch = "email"
	}
	return ch
}

func (o opts) agentArg() string { return strings.Join(o.pos, " ") }

func confirm(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [type yes to proceed]: ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	return strings.TrimSpace(strings.ToLower(sc.Text())) == "yes"
}

// backup writes the pre-change object to ~/ft_backup_YYYYMMDD/ before any write,
// mirroring the Zendesk backup rule.
func backup(kind, name string, obj any) string {
	dir := filepath.Join(home(), "ft_backup_"+time.Now().Format("20060102"))
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, fmt.Sprintf("%s__%s__%s.json", safeName(name), kind, time.Now().Format("150405")))
	// round-trip through JSON so redact sees plain maps, then drop secrets:
	// backups are for reading and diffing, never a place credentials live
	var plain any
	raw, _ := json.Marshal(obj)
	json.Unmarshal(raw, &plain)
	plain, _ = redact(plain)
	b, _ := json.MarshalIndent(plain, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		fail(fmt.Errorf("backup failed, not writing: %w", err))
	}
	return p
}

// lineDiff prints a minimal unified-style diff of two texts (LCS on lines).
func lineDiff(a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []string
	i, j, changed := 0, 0, false
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, fmt.Sprintf("\x1b[32m+ %s\x1b[0m", y[j]))
			j, changed = j+1, true
		default:
			out = append(out, fmt.Sprintf("\x1b[31m- %s\x1b[0m", x[i]))
			i, changed = i+1, true
		}
	}
	if !changed {
		return "(no changes)"
	}
	return strings.Join(out, "\n")
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// dryRunNote is printed when a write ran without --apply.
func dryRunNote(cmd string) {
	fmt.Fprintf(os.Stderr, "\nDry run: nothing written. Re-run with --apply to write the draft (%s).\n", cmd)
}

func getMap(v any, keys ...string) map[string]any {
	m, _ := v.(map[string]any)
	for _, k := range keys {
		if m == nil {
			return nil
		}
		m, _ = m[k].(map[string]any)
	}
	return m
}

func getArr(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if a, ok := m[k].([]any); ok {
			return a
		}
	}
	return nil
}

func decodeJSON(b []byte) any {
	var v any
	json.Unmarshal(b, &v)
	return v
}
