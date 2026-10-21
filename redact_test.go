package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const sampleAction = `{
  "action_name": "Order status",
  "action_type": "api",
  "action_fields": {
    "url": "https://api.example.com/orders?id={{order_id}}&api_key=abc123secretvalue",
    "method": "GET",
    "headers": {"Content-Type": "application/json", "X-Forethought-Api-Key": "ft_live_abcdefgh12345678", "Authorization": "Bearer {{token}}"},
    "auth_token": "tok_9f8e7d6c5b4a",
    "authorization_type": "none",
    "authorization_config_id": "cfg_123",
    "body_parameters": {"query": "{{question}}"},
    "jmespath_query": "order.status",
    "output_context_variables": {"stats": {"lastSessions": ["sessions[0].endedAt"]}, "attendedSession": "profile.attended"},
    "tags": ["Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.sig"]
  }
}`

func decodeT(t *testing.T, s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRedactHidesSecretsKeepsSettings(t *testing.T) {
	r, n := redact(decodeT(t, sampleAction))
	out := jsonText(r)
	for _, leak := range []string{"abc123secretvalue", "ft_live_abcdefgh12345678", "tok_9f8e7d6c5b4a", "eyJhbGciOiJIUzI1NiJ9"} {
		if strings.Contains(out, leak) {
			t.Errorf("secret %q leaked:\n%s", leak, out)
		}
	}
	for _, keep := range []string{`"none"`, `"cfg_123"`, `"application/json"`, `Bearer {{token}}`, `{{question}}`, `id={{order_id}}`, `order.status`, `sessions[0].endedAt`, `profile.attended`} {
		if !strings.Contains(out, keep) {
			t.Errorf("non-secret %s was redacted:\n%s", keep, out)
		}
	}
	if n != 4 {
		t.Errorf("redacted %d values, want 4", n)
	}
}

func TestRestoreSecretsRoundTrip(t *testing.T) {
	cur := decodeT(t, sampleAction)
	red, _ := redact(cur)
	// user edits a non-secret field and leaves the marks alone
	m := red.(map[string]any)
	m["action_fields"].(map[string]any)["jmespath_query"] = "order.state"
	got, err := restoreSecrets(red, cur)
	if err != nil {
		t.Fatal(err)
	}
	want := decodeT(t, strings.Replace(sampleAction, "order.status", "order.state", 1))
	if jsonText(got) != jsonText(want) {
		t.Errorf("restore mismatch:\n%s\nwant\n%s", jsonText(got), jsonText(want))
	}
}

func TestRestoreRejectsMarkWithoutSource(t *testing.T) {
	cur := decodeT(t, sampleAction)
	next := decodeT(t, strings.Replace(sampleAction, `"method": "GET",`, `"method": "GET", "new_header_key": "[redacted]",`, 1))
	if _, err := restoreSecrets(next, cur); err == nil {
		t.Error("a [redacted] mark with no current value was accepted")
	}
}

func TestRestoreRejectsEditedAroundMark(t *testing.T) {
	cur := decodeT(t, sampleAction)
	red, _ := redact(cur)
	f := red.(map[string]any)["action_fields"].(map[string]any)
	f["url"] = strings.Replace(f["url"].(string), "https://api.example.com", "https://evil.example.net", 1)
	if _, err := restoreSecrets(red, cur); err == nil {
		t.Error("changing the URL host around a redacted key was accepted")
	}
}

func TestEnvRefs(t *testing.T) {
	t.Setenv("FT_TEST_KEY", "s3cr3t")
	v, used, err := resolveEnvRefs(decodeT(t, `{"headers":{"X-Key":"${env:FT_TEST_KEY}"}}`))
	if err != nil || jsonText(v) != jsonText(decodeT(t, `{"headers":{"X-Key":"s3cr3t"}}`)) || len(used) != 1 {
		t.Fatalf("got %v %v %v", jsonText(v), used, err)
	}
	if _, _, err := resolveEnvRefs(decodeT(t, `{"k":"${env:FT_TEST_MISSING_X}"}`)); err == nil {
		t.Error("missing env var accepted")
	}
	// a ${env:…} ref is never shown as a secret, so diffs stay readable
	if r, n := redact(decodeT(t, `{"headers":{"X-Key":"${env:FT_TEST_KEY}"}}`)); n != 0 || !strings.Contains(jsonText(r), "${env:FT_TEST_KEY}") {
		t.Errorf("env ref was redacted: %s", jsonText(r))
	}
}
