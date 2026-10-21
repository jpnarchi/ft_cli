package main

import (
	"strings"
	"testing"
)

const samplePolicy = `<p><strong>Role</strong></p><ul><li>Point them to self-serve first.</li><li>If unclear, reroute to <span data-type="intent-reroute" data-id="Handoff" data-label="Standard Handoff">Standard Handoff</span>.</li></ul><p>Check <span data-type="context-variable" data-id="cv1" data-label="Persona Verification Passed">Persona Verification Passed</span> before Template B.</p><ol><li>Step one</li><li>Step two</li></ol>`

func TestChipRoundTrip(t *testing.T) {
	text, cm := policyToText(samplePolicy)
	for _, want := range []string{"{{reroute: Standard Handoff}}", "{{var: Persona Verification Passed}}", "* Point them", "1. Step one", "2. Step two", "**Role**"} {
		if !strings.Contains(text, want) {
			t.Fatalf("readable text missing %q:\n%s", want, text)
		}
	}
	html, err := textToPolicy(text, cm, nil)
	if err != nil {
		t.Fatal(err)
	}
	for el := range map[string]bool{cm["{{reroute: Standard Handoff}}"]: true, cm["{{var: Persona Verification Passed}}"]: true} {
		if !strings.Contains(html, el) {
			t.Fatalf("chip element not restored byte-for-byte: %s\nin: %s", el, html)
		}
	}
	again, _ := policyToText(html)
	if again != text {
		t.Fatalf("round trip changed the text:\n--- first\n%s\n--- second\n%s", text, again)
	}
}

// realPolicy uses the chip markup seen live on 2026-10-21.
const realPolicy = `<p>If <span class="context-variable-node" data-flow-id="flow_abc123" id="1a2b3c4d_0000_4000_8000_000000000001" name="Persona Verification Passed"></span> is false, reroute to <span class="reroute-node" data-flow-id="flow_def456" id="fallback" name="Fallback" type="hand_off"></span>.</p>`

func TestRealChipFormat(t *testing.T) {
	text, cm := policyToText(realPolicy)
	for _, want := range []string{"{{var: Persona Verification Passed}}", "{{reroute: Fallback}}"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	h, err := textToPolicy(text, cm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, cm["{{reroute: Fallback}}"]) || !strings.Contains(h, cm["{{var: Persona Verification Passed}}"]) {
		t.Fatalf("chips not restored byte for byte:\n%s", h)
	}
}

func TestNewChipFromMenu(t *testing.T) {
	_, cm := policyToText(realPolicy)
	targets := []mentionTarget{{Kind: "var", Label: "Age", ID: "1a2b3c4d_0000_4000_8000_000000000002"}}

	// new variable: cloned from the variable chip, new id/name/flow id
	h, err := textToPolicy("Check {{var: Age}}.\n", cm, targets)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, ` id="1a2b3c4d_0000_4000_8000_000000000002"`) || !strings.Contains(h, ` name="Age"`) ||
		!strings.Contains(h, `class="context-variable-node"`) || strings.Contains(h, "flow_abc123") {
		t.Fatalf("new variable chip wrong: %s", h)
	}

	// a second reroute to an existing destination reuses that chip with a fresh flow id
	text, _ := policyToText(realPolicy)
	h, err = textToPolicy(text+"\nAlso {{reroute: Fallback #2}}.\n", cm, targets)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(h, `name="Fallback" type="hand_off"`) != 2 || strings.Count(h, "flow_def456") != 1 {
		t.Fatalf("reroute not reused with a fresh flow id: %s", h)
	}

	// reroute to a destination not in the policy, or an unknown variable: refused
	for _, bad := range []string{"{{reroute: Billing}}\n", "{{var: Nope}}\n"} {
		if _, err := textToPolicy(bad, cm, targets); err == nil {
			t.Errorf("expected an unresolved-chip error for %q", bad)
		}
	}
}

// editorPolicy copies the live editor markup: classes, flow ids, spans,
// spacer paragraphs, an ol continuing at 5, and a raw apostrophe.
const editorPolicy = `<h2 class="editor-heading-h2" data-flow-id="flow_aaaaaa"><span>Steps</span></h2>` +
	`<p class="editor-paragraph" data-flow-id="flow_bbbbbb"><span>Read the contributor's message.</span></p>` +
	`<p class="editor-paragraph" data-flow-id="flow_cccccc"><br/></p>` +
	`<ol class="editor-list-ol" data-flow-id="flow_dddddd" start="5"><li class="editor-listitem" data-flow-id="flow_eeeeee" value="5"><span>Check </span><span class="context-variable-node" data-flow-id="flow_ffffff" id="cv_1" name="Persona Verification Passed"></span></li>` +
	`<li class="editor-listitem" data-flow-id="flow_gggggg" value="6"><span>Reply.</span></li></ol>` +
	`<p class="editor-paragraph" data-flow-id="flow_hhhhhh"><span>Do not reroute to </span><strong>Fallback</strong><span> here.</span></p>`

func TestPreserveBlocksUnchanged(t *testing.T) {
	text, cm := policyToText(editorPolicy)
	if !strings.Contains(text, "5. Check {{var: Persona Verification Passed}}") || !strings.Contains(text, "6. Reply.") {
		t.Fatalf("ol start not honored:\n%s", text)
	}
	gen, err := textToPolicy(text, cm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, rebuilt := preserveBlocks(editorPolicy, gen); out != editorPolicy || rebuilt != 0 {
		t.Fatalf("unchanged policy not byte for byte (rebuilt %d):\n%s", rebuilt, out)
	}
}

func TestPreserveBlocksOneEdit(t *testing.T) {
	text, cm := policyToText(editorPolicy)
	edited := strings.Replace(text, "Reply.", "Reply politely.", 1)
	gen, err := textToPolicy(edited, cm, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, kept, rebuilt := preserveBlocks(editorPolicy, gen)
	if kept != 3 || rebuilt != 1 {
		t.Fatalf("kept %d rebuilt %d, want 3 and 1", kept, rebuilt)
	}
	for _, same := range []string{"flow_aaaaaa", "flow_bbbbbb", "flow_cccccc", "flow_hhhhhh", `contributor's`} {
		if !strings.Contains(out, same) {
			t.Errorf("untouched block lost %s", same)
		}
	}
	if !strings.Contains(out, `class="editor-list-ol"`) || !strings.Contains(out, `start="5"`) || !strings.Contains(out, `value="6"`) ||
		!strings.Contains(out, `<span>Reply politely.</span>`) || !strings.Contains(out, `id="cv_1" name="Persona Verification Passed"`) {
		t.Errorf("edited block not in editor markup:\n%s", out)
	}
	if got, _ := policyToText(out); got != edited {
		t.Errorf("text after merge differs from the edit:\n%s", lineDiff(edited, got))
	}
}
