// Secret redaction for anything the CLI prints or writes to disk.
//
// Actions carry credentials (API-key headers, auth tokens). The CLI never shows
// or stores them: values are replaced with redactedMark, and on update each
// mark is restored from the server's current value at the same JSON path.
// New credentials are passed as ${env:NAME}, resolved only at send time.
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const redactedMark = "[redacted]"

var (
	reSecretKey = regexp.MustCompile(`(?i)(secret|token|password|passwd|api[_-]?key|apikey|private[_-]?key|access[_-]?key|credential|authorization|cookie|jwt|signature|bearer)`)
	// keys that name a setting rather than hold a secret
	reSecretKeySafe = regexp.MustCompile(`(?i)(_id|_ids|_type|_scope|_integration|_variable|_variables|_name|_label|_url|_enabled|_mode)$`)
	reSecretValue   = regexp.MustCompile(`(?i)^(bearer|basic|token)\s+\S{8,}|^eyJ[\w-]{8,}\.[\w-]{8,}\.|^(sk|pk|rk)_(live|test)_\w{8,}|^xox[abposr]-\w{8,}|^gh[pousr]_\w{20,}`)
	reTemplate      = regexp.MustCompile(`\{\{[^}]*\}\}|\$\{[^}]*\}`)
	reURLSecret     = regexp.MustCompile(`(?i)([?&](?:[\w-]*(?:key|token|secret|signature|password|sig)[\w-]*)=)([^&#]+)`)
	reIndexSuffix   = regexp.MustCompile(`(\[\d+\])+$`)
	reEnvRef        = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)
	safeHeaders     = map[string]bool{"content-type": true, "accept": true, "accept-language": true, "user-agent": true, "accept-encoding": true}
)

// onlyTemplate reports whether s holds no literal secret: just {{vars}},
// ${env:…} refs and an auth scheme word.
func onlyTemplate(s string) bool {
	rest := strings.TrimSpace(reTemplate.ReplaceAllString(s, ""))
	rest = strings.TrimSpace(regexp.MustCompile(`(?i)^(bearer|basic|token)`).ReplaceAllString(rest, ""))
	return rest == ""
}

// redactString returns the redacted form of a string value found under key
// (inHeaders: the value is an HTTP header).
func redactString(key, s string, inHeaders bool) string {
	if s == "" || s == redactedMark || onlyTemplate(s) {
		return s
	}
	switch {
	case inHeaders && !safeHeaders[strings.ToLower(key)]:
		return redactedMark
	case reSecretKey.MatchString(key) && !reSecretKeySafe.MatchString(key):
		return redactedMark
	case reSecretValue.MatchString(strings.TrimSpace(s)):
		return redactedMark
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "/") {
		return reURLSecret.ReplaceAllStringFunc(s, func(m string) string {
			p := reURLSecret.FindStringSubmatch(m)
			if onlyTemplate(p[2]) {
				return m
			}
			return p[1] + redactedMark
		})
	}
	return s
}

// redact returns a deep copy of v with secrets replaced, and how many were.
func redact(v any) (any, int) {
	n := 0
	var walk func(v any, key string, inHeaders bool) any
	walk = func(v any, key string, inHeaders bool) any {
		switch x := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			hdr := strings.EqualFold(key, "headers")
			for k, e := range x {
				out[k] = walk(e, k, hdr)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = walk(e, key, inHeaders)
			}
			return out
		case string:
			r := redactString(key, x, inHeaders)
			if r != x {
				n++
			}
			return r
		}
		return v
	}
	return walk(v, "", false), n
}

// restoreSecrets replaces each redactedMark in next with the value at the same
// path in cur. A string that still has a mark must equal the redacted form of
// the current value, so an edited-around secret can't be silently mixed up.
func restoreSecrets(next, cur any) (any, error) {
	var walk func(n, c any, path string) (any, error)
	walk = func(n, c any, path string) (any, error) {
		switch x := n.(type) {
		case map[string]any:
			cm, _ := c.(map[string]any)
			out := make(map[string]any, len(x))
			for k, e := range x {
				var ce any
				if cm != nil {
					ce = cm[k]
				}
				r, err := walk(e, ce, path+"."+k)
				if err != nil {
					return nil, err
				}
				out[k] = r
			}
			return out, nil
		case []any:
			ca, _ := c.([]any)
			out := make([]any, len(x))
			for i, e := range x {
				var ce any
				if i < len(ca) {
					ce = ca[i]
				}
				r, err := walk(e, ce, fmt.Sprintf("%s[%d]", path, i))
				if err != nil {
					return nil, err
				}
				out[i] = r
			}
			return out, nil
		case string:
			if !strings.Contains(x, redactedMark) {
				return x, nil
			}
			cs, ok := c.(string)
			if !ok {
				return nil, fmt.Errorf("%s is %s but the current action has no secret there; put the value in an env var and use ${env:NAME}", strings.TrimPrefix(path, "."), redactedMark)
			}
			bare := reIndexSuffix.ReplaceAllString(path, "")
			key := bare[strings.LastIndex(bare, ".")+1:]
			inHdr := strings.Contains(path, ".headers.")
			if x == redactedMark || x == redactString(key, cs, inHdr) {
				return cs, nil
			}
			return nil, fmt.Errorf("%s mixes %s with edited text; restore the original line or use ${env:NAME} for the whole value", strings.TrimPrefix(path, "."), redactedMark)
		}
		return n, nil
	}
	return walk(next, cur, "")
}

// resolveEnvRefs swaps ${env:NAME} for the environment value, failing on any
// unset variable. The names used are returned for reporting (never values).
func resolveEnvRefs(v any) (any, []string, error) {
	var used []string
	var missing []string
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k] = walk(e)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = walk(e)
			}
			return out
		case string:
			return reEnvRef.ReplaceAllStringFunc(x, func(m string) string {
				name := reEnvRef.FindStringSubmatch(m)[1]
				val, ok := os.LookupEnv(name)
				if !ok || val == "" {
					missing = append(missing, name)
					return m
				}
				used = append(used, name)
				return val
			})
		}
		return v
	}
	out := walk(v)
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("env var(s) not set: %s", strings.Join(dedup(missing), ", "))
	}
	used = dedup(used)
	sort.Strings(used)
	return out, used, nil
}
