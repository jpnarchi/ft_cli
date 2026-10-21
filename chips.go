// Policy HTML <-> readable text, with chips as {{kind: label}} tokens.
//
// FT stores an autoflow policy as HTML; each "/" chip is an empty span, e.g.
//   <span class="context-variable-node" data-flow-id="flow_abc123" id="<cv id>" name="Persona Verification Passed"></span>
//   <span class="reroute-node" data-flow-id="flow_def456" id="fallback" name="Fallback" type="hand_off"></span>
// (seen live 2026-10-21). On read each chip becomes a readable token and its
// exact HTML is kept in a chip map, so a write puts it back byte for byte.
// A NEW token reuses an existing chip with the same label (fresh data-flow-id),
// or, for variables, clones a variable chip and points it at the "/" menu
// target. Other new kinds (reroutes, actions) need one copy added in the UI,
// because their type attribute can't be derived safely.
package main

import (
	"crypto/rand"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
)

// chipMap maps a readable token to the stored chip HTML.
type chipMap map[string]string

var (
	reChipPair  = regexp.MustCompile(`(?s)<(span|mark|a|button|div)\b([^>]*\bdata-[a-zA-Z-]+=[^>]*)>(.*?)</(?:span|mark|a|button|div)>`)
	reChipVoid  = regexp.MustCompile(`<(span|img|input)\b([^>]*\bdata-[a-zA-Z-]+=[^>]*)/>`)
	reAttr      = regexp.MustCompile(`([a-zA-Z_:-]+)\s*=\s*"([^"]*)"`)
	reToken     = regexp.MustCompile(`\{\{\s*([a-zA-Z_-]+)\s*:\s*([^}]+?)\s*\}\}`)
	reAnyTag    = regexp.MustCompile(`<[^>]+>`)
	reTokenDup  = regexp.MustCompile(`\s+#\d+$`)
)

func attrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range reAttr.FindAllStringSubmatch(s, -1) {
		m[strings.ToLower(a[1])] = html.UnescapeString(a[2])
	}
	return m
}

// chipKind normalizes whatever the element says it is into the spec's kinds.
func chipKind(at map[string]string, tag string) string {
	var hint []string
	for k, v := range at {
		if strings.HasPrefix(k, "data-") || k == "class" {
			hint = append(hint, strings.ToLower(k+"="+v))
		}
	}
	h := strings.Join(hint, " ")
	switch {
	case strings.Contains(h, "reroute") || strings.Contains(h, "handoff") || strings.Contains(h, "transfer"):
		return "reroute"
	case strings.Contains(h, "form"):
		return "form"
	case strings.Contains(h, "context") || strings.Contains(h, "variable") || strings.Contains(h, "cv"):
		return "var"
	case strings.Contains(h, "action") || strings.Contains(h, "tool"):
		return "action"
	case strings.Contains(h, "intent") || strings.Contains(h, "agent") || strings.Contains(h, "workflow"):
		return "agent"
	}
	if t := at["data-type"]; t != "" {
		return strings.ToLower(t)
	}
	return tag
}

func chipLabel(at map[string]string, inner string) string {
	for _, k := range []string{"data-label", "data-name", "data-display-name", "data-title", "name"} {
		if v := strings.TrimSpace(at[k]); v != "" {
			return v
		}
	}
	t := strings.TrimSpace(html.UnescapeString(reAnyTag.ReplaceAllString(inner, "")))
	t = strings.TrimPrefix(t, "${{")
	t = strings.TrimSuffix(t, "}}")
	return strings.TrimSpace(strings.TrimPrefix(t, "/"))
}

// extractChips replaces chip elements with tokens and returns the chip map.
func extractChips(h string) (string, chipMap) {
	cm := chipMap{}
	tokenFor := func(full, tag, attrStr, inner string) string {
		at := attrs(attrStr)
		tok := fmt.Sprintf("{{%s: %s}}", chipKind(at, tag), chipLabel(at, inner))
		for n := 2; cm[tok] != "" && cm[tok] != full; n++ {
			tok = fmt.Sprintf("{{%s: %s #%d}}", chipKind(at, tag), chipLabel(at, inner), n)
		}
		cm[tok] = full
		return "\x00" + tok + "\x00"
	}
	h = reChipPair.ReplaceAllStringFunc(h, func(s string) string {
		m := reChipPair.FindStringSubmatch(s)
		return tokenFor(s, m[1], m[2], m[3])
	})
	h = reChipVoid.ReplaceAllStringFunc(h, func(s string) string {
		m := reChipVoid.FindStringSubmatch(s)
		return tokenFor(s, m[1], m[2], "")
	})
	return h, cm
}

// policyToText renders policy HTML as readable markdown-ish text.
func policyToText(h string) (string, chipMap) {
	h, cm := extractChips(h)
	r := strings.NewReplacer(
		"<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"<strong>", "**", "</strong>", "**", "<b>", "**", "</b>", "**",
		"<em>", "*", "</em>", "*", "<i>", "*", "</i>", "*",
	)
	h = r.Replace(h)
	// block structure
	var out strings.Builder
	var listStack []string // "ul" | "ol"
	olCount := []int{}
	tagRe := regexp.MustCompile(`(?s)<(/?)([a-zA-Z0-9]+)[^>]*>`)
	last := 0
	for _, loc := range tagRe.FindAllStringSubmatchIndex(h, -1) {
		out.WriteString(h[last:loc[0]])
		last = loc[1]
		closing := h[loc[2]:loc[3]] == "/"
		tag := strings.ToLower(h[loc[4]:loc[5]])
		switch tag {
		case "p", "div":
			if closing {
				out.WriteString("\n\n")
			}
		case "h1", "h2", "h3", "h4":
			if !closing {
				out.WriteString("\n" + strings.Repeat("#", int(tag[1]-'0')) + " ")
			} else {
				out.WriteString("\n\n")
			}
		case "ul", "ol":
			if !closing {
				listStack = append(listStack, tag)
				n := 0
				if st := attrs(h[loc[0]:loc[1]])["start"]; st != "" {
					fmt.Sscanf(st, "%d", &n)
					n--
				}
				olCount = append(olCount, n)
			} else if len(listStack) > 0 {
				listStack, olCount = listStack[:len(listStack)-1], olCount[:len(olCount)-1]
				if len(listStack) == 0 {
					out.WriteString("\n\n") // so a following paragraph isn't read as part of the last item
				}
			}
		case "li":
			if !closing {
				depth := len(listStack) - 1
				if depth < 0 {
					depth = 0
				}
				out.WriteString("\n" + strings.Repeat("  ", depth))
				if len(listStack) > 0 && listStack[len(listStack)-1] == "ol" {
					olCount[len(olCount)-1]++
					out.WriteString(fmt.Sprintf("%d. ", olCount[len(olCount)-1]))
				} else {
					out.WriteString("* ")
				}
			}
		}
	}
	out.WriteString(h[last:])
	text := html.UnescapeString(out.String())
	text = strings.ReplaceAll(text, "\x00", "")
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	text = regexp.MustCompile(`(?m)[ \t]+$`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`\n\n(\s*(?:\* |\d+\. ))`).ReplaceAllString(text, "\n$1")
	return strings.TrimSpace(text) + "\n", cm
}

// mentionTarget is one entry of the "/" menu.
type mentionTarget struct {
	Kind, Label, ID string
	Raw             map[string]any
}

// textToPolicy turns readable text back into policy HTML, restoring chips.
func textToPolicy(text string, cm chipMap, targets []mentionTarget) (string, error) {
	// templates: one stored element per kind, for building new chips
	tmpl := map[string]string{}
	for tok, el := range cm {
		if m := reToken.FindStringSubmatch(tok); m != nil {
			if _, ok := tmpl[m[1]]; !ok {
				tmpl[m[1]] = el
			}
		}
	}
	var missing []string
	resolve := func(tok string) string {
		if el, ok := cm[tok]; ok {
			return el
		}
		m := reToken.FindStringSubmatch(tok)
		if m == nil {
			return tok
		}
		kind, label := strings.ToLower(m[1]), strings.TrimSpace(reTokenDup.ReplaceAllString(m[2], ""))
		// same destination already in the policy: copy it with a fresh flow id
		for et, el := range cm {
			if em := reToken.FindStringSubmatch(et); em != nil && strings.EqualFold(em[1], kind) &&
				strings.EqualFold(chipLabel(attrs(el), ""), label) {
				return setAttr(el, "data-flow-id", newFlowID())
			}
		}
		base, hasTmpl := tmpl[kind]
		t, ok := findTarget(targets, kind, label)
		switch {
		case kind != "var":
			missing = append(missing, fmt.Sprintf("%s → new %s chips can't be built safely; add this one in the UI once, then it can be reused", tok, kind))
		case !ok:
			missing = append(missing, fmt.Sprintf("%s → not found in the \"/\" menu (run `ft chips list`)", tok))
		case !hasTmpl:
			missing = append(missing, fmt.Sprintf("%s → no variable chip in this policy yet to copy; add one in the UI once", tok))
		default:
			return cloneChip(base, t)
		}
		return tok
	}
	// escape text but keep tokens
	var parts []string
	last := 0
	for _, loc := range reToken.FindAllStringIndex(text, -1) {
		parts = append(parts, html.EscapeString(text[last:loc[0]]), "\x00"+text[loc[0]:loc[1]]+"\x00")
		last = loc[1]
	}
	parts = append(parts, html.EscapeString(text[last:]))
	esc := strings.Join(parts, "")

	inline := func(s string) string {
		s = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(s, "<strong>$1</strong>")
		s = regexp.MustCompile(`(^|[^*])\*([^*\s][^*]*?)\*`).ReplaceAllString(s, "$1<em>$2</em>")
		return regexp.MustCompile("\x00([^\x00]+)\x00").ReplaceAllStringFunc(s, func(t string) string {
			return resolve(strings.Trim(t, "\x00"))
		})
	}

	var b strings.Builder
	type lvl struct {
		tag    string
		indent int
	}
	var stack []lvl
	closeTo := func(indent int) {
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			b.WriteString("</li></" + stack[len(stack)-1].tag + ">")
			stack = stack[:len(stack)-1]
		}
	}
	var para []string
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, "<br>")) + "</p>")
			para = nil
		}
	}
	liRe := regexp.MustCompile(`^(\s*)(\*|-|\d+\.)\s+(.*)$`)
	hRe := regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
	for _, line := range strings.Split(strings.TrimRight(esc, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			closeTo(0)
			continue
		}
		if m := hRe.FindStringSubmatch(line); m != nil {
			flush()
			closeTo(0)
			n := len(m[1])
			b.WriteString(fmt.Sprintf("<h%d>%s</h%d>", n, inline(m[2]), n))
			continue
		}
		if m := liRe.FindStringSubmatch(line); m != nil {
			flush()
			indent := len(m[1])
			tag, open := "ul", "<ul><li>"
			if m[2] != "*" && m[2] != "-" {
				tag, open = "ol", "<ol><li>"
				if n := strings.TrimSuffix(m[2], "."); n != "1" {
					open = `<ol start="` + n + `"><li>` // a list continuing earlier numbering
				}
			}
			if len(stack) > 0 && stack[len(stack)-1].indent == indent && stack[len(stack)-1].tag == tag {
				b.WriteString("</li><li>")
			} else if len(stack) > 0 && stack[len(stack)-1].indent < indent {
				b.WriteString(open)
				stack = append(stack, lvl{tag, indent})
			} else {
				closeTo(indent)
				if len(stack) > 0 && stack[len(stack)-1].indent == indent && stack[len(stack)-1].tag == tag {
					b.WriteString("</li><li>")
				} else {
					b.WriteString(open)
					stack = append(stack, lvl{tag, indent})
				}
			}
			b.WriteString(inline(m[3]))
			continue
		}
		if len(stack) > 0 { // continuation line inside a list item
			b.WriteString("<br>" + inline(strings.TrimSpace(line)))
			continue
		}
		para = append(para, line)
	}
	flush()
	closeTo(0)
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("unresolved chips:\n  %s", strings.Join(missing, "\n  "))
	}
	return b.String(), nil
}

func findTarget(ts []mentionTarget, kind, label string) (mentionTarget, bool) {
	ll := strings.ToLower(label)
	for _, t := range ts {
		if t.Kind == kind && strings.ToLower(t.Label) == ll {
			return t, true
		}
	}
	for _, t := range ts { // kind names may differ slightly; fall back to label
		if strings.ToLower(t.Label) == ll {
			return t, true
		}
	}
	return mentionTarget{}, false
}

// cloneChip points a template chip at a new target: id and name change, and it
// gets its own data-flow-id. Older data-* style chips are handled too.
func cloneChip(el string, t mentionTarget) string {
	out := el
	if strings.Contains(el, ` name="`) {
		out = setAttr(out, "id", t.ID)
		out = setAttr(out, "name", t.Label)
	}
	for k, v := range attrs(el) {
		if !strings.HasPrefix(k, "data-") || k == "data-flow-id" {
			continue
		}
		switch {
		case strings.Contains(k, "label") || strings.Contains(k, "name") || strings.Contains(k, "title"):
			out = strings.Replace(out, fmt.Sprintf(`%s="%s"`, k, html.EscapeString(v)), fmt.Sprintf(`%s="%s"`, k, html.EscapeString(t.Label)), 1)
		case strings.HasSuffix(k, "id"):
			out = strings.Replace(out, fmt.Sprintf(`%s="%s"`, k, v), fmt.Sprintf(`%s="%s"`, k, t.ID), 1)
		}
	}
	if strings.Contains(el, ` data-flow-id="`) {
		out = setAttr(out, "data-flow-id", newFlowID())
	}
	if m := reChipPair.FindStringSubmatch(el); m != nil {
		if inner := strings.TrimSpace(m[3]); inner != "" && !strings.Contains(inner, "<") {
			out = strings.Replace(out, ">"+inner+"<", ">"+html.EscapeString(t.Label)+"<", 1)
		}
	}
	return out
}

// setAttr replaces one attribute's value; the leading space keeps "id" from
// matching "data-flow-id".
func setAttr(el, name, val string) string {
	re := regexp.MustCompile(`(\s` + regexp.QuoteMeta(name) + `=")[^"]*(")`)
	return re.ReplaceAllString(el, "${1}"+strings.ReplaceAll(html.EscapeString(val), "$", "$$")+"${2}")
}

func newFlowID() string {
	const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return "flow_" + string(b)
}
