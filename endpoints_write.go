// Email preview endpoints (confirmed 2026-10-21):
//
//	start : POST /solve/email/preview              multipart
//	reply : POST /solve/email/preview/{sid}/reply  JSON
package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
)

const epPreviewStart = "/solve/email/preview"

// startPreview opens a preview thread. a.ID == "" means "auto": no intent
// filter, so FT predicts the agent like it would for a real email.
func startPreview(c creds, a agent, query string, mockActions bool) (previewResult, error) {
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
	if mockActions {
		fields["mock_actions"] = "true"
		fields["create_real_tickets"] = "false"
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

func continuePreview(c creds, sid, query string, mockActions bool) (previewResult, error) {
	payload := map[string]any{
		"preview_session_id":                 sid,
		"reply_text":                         query,
		"custom_css":                         "",
		"ai_instruction_filters_for_preview": []any{},
	}
	if mockActions {
		payload["mock_actions"] = true
		payload["create_real_tickets"] = false
	}
	body, _ := json.Marshal(payload)
	b, err := apiSendRaw(c, "POST", epPreviewStart+"/"+sid+"/reply", "application/json", body)
	if err != nil {
		return previewResult{}, err
	}
	return parsePreview(b), nil
}
