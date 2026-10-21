// Email preview endpoints (confirmed 2026-10-21):
//   start : POST /solve/email/preview              multipart
//   reply : POST /solve/email/preview/{sid}/reply  JSON
// The dashboard's preview has no option to mock actions (checked in its bundle
// 2026-10-21; mocks exist only in FT's separate agent test cases), so a
// preview runs the agent's real actions. cmdPreview gates on that.
package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
)

const epPreviewStart = "/solve/email/preview"

// startPreview opens a preview thread. a.ID == "" means "auto": no intent
// filter, so FT predicts the agent like it would for a real email.
func startPreview(c creds, a agent, query string) (previewResult, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := map[string]string{
		"query":                              query,
		"subject":                            "Preview test",
		"custom_css":                         "",
		"ai_instruction_filters_for_preview": "[]",
		"is_preview":                         "true",
	}
	if a.ID != "" {
		fields["intent_definition_id"] = a.ID
		fields["email_workflow_id"] = a.Workflow
		fields["subject"] = "Preview test: " + a.Name
	}
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	b, err := apiSendRaw(c, "POST", epPreviewStart, w.FormDataContentType(), buf.Bytes())
	if err != nil {
		return previewResult{}, err
	}
	return parsePreview(b), nil
}

func continuePreview(c creds, sid, query string) (previewResult, error) {
	payload := map[string]any{
		"preview_session_id":                 sid,
		"reply_text":                         query,
		"custom_css":                         "",
		"ai_instruction_filters_for_preview": []any{},
	}
	body, _ := json.Marshal(payload)
	b, err := apiSendRaw(c, "POST", epPreviewStart+"/"+sid+"/reply", "application/json", body)
	if err != nil {
		return previewResult{}, err
	}
	return parsePreview(b), nil
}
