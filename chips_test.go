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

func TestNewChipFromMenu(t *testing.T) {
	_, cm := policyToText(samplePolicy)
	targets := []mentionTarget{{Kind: "agent", Label: "Fallback", ID: "Fallback"}}
	html, err := textToPolicy("Otherwise {{reroute: Fallback}}.\n", cm, targets)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `data-id="Fallback"`) || !strings.Contains(html, ">Fallback<") {
		t.Fatalf("new chip not built from template: %s", html)
	}
	if _, err := textToPolicy("{{form: Nope}}\n", cm, targets); err == nil {
		t.Fatal("expected an unresolved-chip error")
	}
}
