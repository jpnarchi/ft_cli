// Block-preserving policy writes.
//
// FT's policy editor (Lexical) stores markup like
//
//	<p class="editor-paragraph" data-flow-id="flow_ghi789"><span>text</span></p>
//	<ol class="editor-list-ol" data-flow-id="…" start="5"><li class="editor-listitem" data-flow-id="…" value="5">…
//
// The data-flow-ids may tie blocks to FT's flow diagram, so set-policy keeps
// every top-level block whose content didn't change byte for byte, and only
// rebuilds edited or new blocks, in the editor's own markup.
package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	reOpenTag   = regexp.MustCompile(`^<([a-zA-Z][a-zA-Z0-9]*)\b[^>]*?(/?)>`)
	reCloseTag  = regexp.MustCompile(`^</([a-zA-Z][a-zA-Z0-9]*)\s*>`)
	reChipSpan  = regexp.MustCompile(`<span\s[^>]*></span>`)
	reEditorAtt = regexp.MustCompile(`\s(?:class|data-flow-id|value|start)="[^"]*"`)
	reBr        = regexp.MustCompile(`<br\s*/?>`)
	reSpaces    = regexp.MustCompile(`\s+`)
	voidTags    = map[string]bool{"br": true, "img": true, "hr": true, "input": true}
)

// splitBlocks splits HTML into its top-level elements (text between them is
// kept as its own block so nothing is lost).
func splitBlocks(h string) []string {
	var out []string
	i := 0
	for i < len(h) {
		if h[i] != '<' {
			j := strings.IndexByte(h[i:], '<')
			if j < 0 {
				j = len(h) - i
			}
			if s := h[i : i+j]; strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
			i += j
			continue
		}
		end := elementEnd(h, i)
		out = append(out, h[i:end])
		i = end
	}
	return out
}

// elementEnd returns the index just past the element starting at h[i].
func elementEnd(h string, i int) int {
	m := reOpenTag.FindStringSubmatch(h[i:])
	if m == nil {
		if j := strings.IndexByte(h[i:], '>'); j >= 0 {
			return i + j + 1
		}
		return len(h)
	}
	name := strings.ToLower(m[1])
	pos := i + len(m[0])
	if m[2] == "/" || voidTags[name] {
		return pos
	}
	depth := 1
	for pos < len(h) {
		j := strings.IndexByte(h[pos:], '<')
		if j < 0 {
			return len(h)
		}
		pos += j
		if c := reCloseTag.FindStringSubmatch(h[pos:]); c != nil {
			if strings.EqualFold(c[1], name) {
				depth--
			}
			pos += len(c[0])
			if depth == 0 {
				return pos
			}
			continue
		}
		if o := reOpenTag.FindStringSubmatch(h[pos:]); o != nil {
			if strings.EqualFold(o[1], name) && o[2] != "/" {
				depth++
			}
			pos += len(o[0])
			continue
		}
		pos++
	}
	return len(h)
}

// normBlock reduces a block to its content: editor attributes, plain spans
// and <br> spelling don't count; chips are compared without their flow id.
func normBlock(b string) string {
	var chips []string
	b = reChipSpan.ReplaceAllStringFunc(b, func(s string) string {
		chips = append(chips, regexp.MustCompile(`\sdata-flow-id="[^"]*"`).ReplaceAllString(s, ""))
		return fmt.Sprintf("\x01%d\x01", len(chips)-1)
	})
	b = reEditorAtt.ReplaceAllString(b, "")
	b = strings.NewReplacer("<span>", "", "</span>", "").Replace(b)
	b = reBr.ReplaceAllString(b, "<br>")
	b = html.UnescapeString(b) // &#39; vs ' and the like
	b = strings.TrimSpace(reSpaces.ReplaceAllString(b, " "))
	for i, c := range chips {
		b = strings.Replace(b, fmt.Sprintf("\x01%d\x01", i), c, 1)
	}
	return b
}

var reSpacer = regexp.MustCompile(`^<p\b[^>]*>\s*(?:<br\s*/?>)?\s*</p>$`)

type origUnit struct{ html, key, trail string }

// preserveBlocks merges gen (HTML rebuilt from edited text) into orig, block by
// block in order: blocks whose content is unchanged come back byte for byte,
// edited or new ones are rebuilt in editor markup. Empty spacer paragraphs,
// which the text form can't show, stay attached to the block before them.
func preserveBlocks(orig, gen string) (out string, kept, rebuilt int) {
	var lead string
	var units []origUnit
	for _, b := range splitBlocks(orig) {
		if reSpacer.MatchString(strings.TrimSpace(b)) {
			if len(units) == 0 {
				lead += b
			} else {
				units[len(units)-1].trail += b
			}
			continue
		}
		units = append(units, origUnit{html: b, key: normBlock(b)})
	}
	var gb, gk []string
	for _, b := range splitBlocks(gen) {
		if reSpacer.MatchString(strings.TrimSpace(b)) {
			continue
		}
		gb, gk = append(gb, b), append(gk, normBlock(b))
	}
	// LCS on block content
	n, m := len(units), len(gb)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if units[i].key == gk[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else if l[i+1][j] >= l[i][j+1] {
				l[i][j] = l[i+1][j]
			} else {
				l[i][j] = l[i][j+1]
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(lead)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && units[i].key == gk[j]:
			sb.WriteString(units[i].html + units[i].trail)
			kept++
			i, j = i+1, j+1
		case i < n && j < m && l[i+1][j+1] == l[i][j]: // edited in place: keep its spacing
			sb.WriteString(editorize(gb[j]) + units[i].trail)
			rebuilt++
			i, j = i+1, j+1
		case j < m && (i == n || l[i][j+1] >= l[i+1][j]): // inserted
			sb.WriteString(editorize(gb[j]))
			rebuilt++
			j++
		default: // deleted
			i++
		}
	}
	return sb.String(), kept, rebuilt
}

var editorClass = map[string]string{
	"p": "editor-paragraph", "h1": "editor-heading-h1", "h2": "editor-heading-h2", "h3": "editor-heading-h3",
	"h4": "editor-heading-h4", "ul": "editor-list-ul", "ol": "editor-list-ol", "li": "editor-listitem",
}

// editorize rewrites a block built by textToPolicy into editor markup: class,
// a fresh data-flow-id, li values, <br/>, and text runs wrapped in <span>.
func editorize(b string) string {
	var sb strings.Builder
	type frame struct {
		tag string
		li  int
	}
	var stack []frame
	inline := 0 // depth inside strong/em/a/chip spans, where text isn't wrapped
	i := 0
	for i < len(b) {
		if b[i] != '<' {
			j := strings.IndexByte(b[i:], '<')
			if j < 0 {
				j = len(b) - i
			}
			text := b[i : i+j]
			if inline == 0 && len(stack) > 0 && stack[len(stack)-1].tag != "ul" && stack[len(stack)-1].tag != "ol" && text != "" {
				sb.WriteString("<span>" + text + "</span>")
			} else {
				sb.WriteString(text)
			}
			i += j
			continue
		}
		if c := reCloseTag.FindStringSubmatch(b[i:]); c != nil {
			name := strings.ToLower(c[1])
			if editorClass[name] != "" && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			} else if inline > 0 {
				inline--
			}
			sb.WriteString(c[0])
			i += len(c[0])
			continue
		}
		o := reOpenTag.FindStringSubmatch(b[i:])
		if o == nil {
			sb.WriteByte(b[i])
			i++
			continue
		}
		name := strings.ToLower(o[1])
		switch {
		case name == "br":
			sb.WriteString("<br/>")
		case editorClass[name] != "":
			attrs := fmt.Sprintf(` class="%s" data-flow-id="%s"`, editorClass[name], newFlowID())
			f := frame{tag: name}
			if st := regexp.MustCompile(`\sstart="(\d+)"`).FindStringSubmatch(o[0]); st != nil && name == "ol" {
				attrs += ` start="` + st[1] + `"`
				fmt.Sscanf(st[1], "%d", &f.li)
				f.li--
			}
			if name == "li" && len(stack) > 0 {
				stack[len(stack)-1].li++
				attrs += fmt.Sprintf(` value="%d"`, stack[len(stack)-1].li)
			}
			sb.WriteString("<" + name + attrs + ">")
			stack = append(stack, f)
		default:
			sb.WriteString(o[0])
			if o[2] != "/" && !voidTags[name] {
				inline++
			}
		}
		i += len(o[0])
	}
	return sb.String()
}
