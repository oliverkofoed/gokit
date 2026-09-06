package llm

// The google-gemini column of the cassette matrix (SPEC §12.2). Scenarios
// live in cassette_suite_test.go.
//
//	GEMINI_API_KEY=... RECORD=1 go test ./llm/ -run TestCassetteGemini -count=1

import (
	"strings"
	"testing"
)

func TestCassetteGemini(t *testing.T) {
	runCassetteSuite(t, casProvider{
		Name:    "gemini",
		EnvKey:  "GEMINI_API_KEY",
		Model:   Gemini37Flash,
		Foreign: ClaudeHaiku45,
		// Temp stays nil: gemini 3.x deprecated the sampling parameters.
		//
		// NoToolCallIDs stays off: 3.x issues a real id on every functionCall
		// part, so pairing is by id like everywhere else. (2.5 and earlier
		// omitted it — that is what R18's call_<n> synthesis is for.)
		//
		// 3.x returns no thought part at all: the signature comes back on the
		// content block, so there is no readable reasoning to assert.
		NoThinkingText:  true,
		NoDocumentName:  true, // inlineData has no field for a file name
		MinOutputTokens: 2048, // 3.7 flash thinks by default and would spend a small cap on it
		Decode:          gemDecodeCassette,
		BadRequest: func(m Model) Request {
			// Gemini clamps an oversized maxOutputTokens instead of rejecting
			// it, so force the error on the model id, which rides in the URL.
			bad := m
			bad.ID = "gemini-nonexistent-model"
			return Request{Model: bad, Messages: []Message{UserText("hi")}, MaxTokens: 256}
		},
		Extra: map[string]func(*testing.T, []map[string]any){
			"thinking": func(t *testing.T, bodies []map[string]any) {
				// R14 on 3.x: the effort level goes over verbatim, nested in
				// thinkingConfig where the budget used to live. Note this is
				// the generateContent shape — the Interactions API spells the
				// same controls generation_config.thinking_level.
				cfg := casMap(bodies[0]["generationConfig"])
				tc := casMap(cfg["thinkingConfig"])
				if tc == nil {
					t.Fatalf("no thinkingConfig: %v", cfg)
				}
				if got := casStr(tc["thinkingLevel"]); got != "low" {
					t.Errorf("thinkingLevel = %q, want low for EffortLow", got)
				}
				if tc["includeThoughts"] != true {
					t.Errorf("includeThoughts = %v, want true", tc["includeThoughts"])
				}
				// Sending both reasoning controls is itself a 400.
				if _, has := tc["thinkingBudget"]; has {
					t.Error("2.5-era thinkingBudget sent alongside thinkingLevel")
				}
				if _, has := cfg["temperature"]; has {
					t.Error("temperature must be omitted on gemini 3.x (deprecated)")
				}
			},
			"tool_call": func(t *testing.T, bodies []map[string]any) {
				// Gemini rejects these JSON Schema constructs, so the
				// sanitizer must have stripped them before they hit the wire.
				raw := string(mustCasJSON(t, bodies[0]["tools"]))
				for _, banned := range []string{"additionalProperties", "$schema"} {
					if strings.Contains(raw, banned) {
						t.Errorf("schema sanitizer left %q on the wire: %s", banned, raw)
					}
				}
			},
		},
	})
}

// gemDecodeCassette reads a streamGenerateContent body. Streaming is selected
// by the URL (?alt=sse), not a body field, so the URL is what gets checked.
func gemDecodeCassette(t *testing.T, url string, body map[string]any) casView {
	if !strings.Contains(url, "alt=sse") {
		t.Errorf("url %q does not request SSE streaming", url)
	}
	if !strings.Contains(url, ":streamGenerateContent") {
		t.Errorf("url %q is not the streaming endpoint", url)
	}

	cfg := casMap(body["generationConfig"])
	v := casView{
		MaxTokens: casInt(cfg["maxOutputTokens"]),
		Stream:    true, // asserted on the URL above
	}
	// The model id lives in the URL path: .../models/<id>:streamGenerateContent
	if _, after, ok := strings.Cut(url, "/models/"); ok {
		v.Model, _, _ = strings.Cut(after, ":")
	}
	_, v.HasTemp = cfg["temperature"]
	if tc := casMap(cfg["thinkingConfig"]); tc != nil {
		v.Thinking = &casThinking{Enabled: true, Budget: casInt(tc["thinkingBudget"])}
	} else if lvl := casStr(cfg["thinking_level"]); lvl != "" {
		v.Thinking = &casThinking{Enabled: true, Effort: lvl}
	}

	for _, part := range casList(casMap(body["systemInstruction"])["parts"]) {
		v.System += casStr(casMap(part)["text"])
	}
	// Tools are one entry holding all function declarations.
	for _, raw := range casList(body["tools"]) {
		for _, rawd := range casList(casMap(raw)["functionDeclarations"]) {
			d := casMap(rawd)
			v.Tools = append(v.Tools, casTool{
				Name:        casStr(d["name"]),
				Description: casStr(d["description"]),
				Schema:      casMap(d["parameters"]),
			})
		}
	}

	for _, raw := range casList(body["contents"]) {
		c := casMap(raw)
		msg := casMessage{Role: casStr(c["role"])}
		if msg.Role == "model" {
			msg.Role = "assistant"
		}
		for _, rawp := range casList(c["parts"]) {
			part := casMap(rawp)
			switch {
			case casMap(part["inlineData"]) != nil:
				// One part type carries both; the mime type is the whole of
				// the difference on this protocol (§8.4), and there is
				// nowhere on it to put a file name.
				blob := casMap(part["inlineData"])
				mime, data := casStr(blob["mimeType"]), casStr(blob["data"])
				if strings.HasPrefix(mime, "image/") {
					msg.Images = append(msg.Images, casImage{MimeType: mime, Data: data})
				} else {
					msg.Documents = append(msg.Documents, casDocument{MimeType: mime, Data: data})
				}
			case casMap(part["functionCall"]) != nil:
				call := casMap(part["functionCall"])
				msg.ToolCalls = append(msg.ToolCalls, casToolCall{
					ID:   casStr(call["id"]),
					Name: casStr(call["name"]),
					Args: string(mustCasJSON(t, call["args"])),
				})
			case casMap(part["functionResponse"]) != nil:
				resp := casMap(part["functionResponse"])
				msg.ToolResults = append(msg.ToolResults, casToolResult{
					ID:   casStr(resp["id"]),
					Name: casStr(resp["name"]),
					Text: casStr(casMap(resp["response"])["output"]),
				})
			case casBool(part["thought"]):
				msg.Thinking = append(msg.Thinking, casThinkingBlock{
					Text: casStr(part["text"]), Signature: casStr(part["thoughtSignature"]),
				})
			default:
				msg.Text += casStr(part["text"])
			}
		}
		v.Messages = append(v.Messages, msg)
	}
	return v
}
