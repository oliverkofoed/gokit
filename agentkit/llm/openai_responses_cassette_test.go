package llm

// The openai-responses column of the cassette matrix (SPEC §12.2). Scenarios
// live in cassette_suite_test.go.
//
//	OPENAI_API_KEY=sk-... RECORD=1 go test ./llm/ -run TestCassetteOpenAIResponses -count=1

import (
	"testing"
)

// oairLunaModel is GPT-5.6 Luna, the smallest model of OpenAI's latest
// family: $0.20/$1.20 per Mtok ($0.02 cached), 1.05M context, 128k output,
// with reasoning, vision and function calling. Not in the catalog — a Model
// literal is all it takes (P2: models are config, not a catalog).
var oairLunaModel = Model{
	ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 0.20, Output: 1.20, CacheRead: 0.02},
	ContextWindow: 1_050_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
}

func TestCassetteOpenAIResponses(t *testing.T) {
	runCassetteSuite(t, casProvider{
		Name:   "openai_responses",
		EnvKey: "OPENAI_API_KEY",
		Model:  oairLunaModel,
		// No CacheModel: OpenAI caches long prefixes automatically, with no
		// breakpoints to place (R15 is an anthropic/openrouter concern).
		Foreign: ClaudeHaiku45,
		// Temp stays nil: the GPT-5 family rejects any temperature but 1.
		//
		// The endpoint streams reasoning summaries only when asked for them;
		// we ask only for encrypted content, so there is a signature to
		// replay but no readable reasoning text.
		NoThinkingText: true,
		// At EffortLow luna returns reasoning_tokens:0 and no reasoning item
		// at all, leaving nothing to replay. High effort is what makes the
		// thinking scenarios exercise anything here.
		ThinkingEffort:  EffortHigh,
		MinOutputTokens: 2048, // luna reasons by default; a small cap yields no text
		Decode:          oairDecodeCassette,
		BadRequest: func(m Model) Request {
			// /responses clamps an oversized max_output_tokens rather than
			// rejecting it (unlike /chat/completions), so force the error on
			// the model id instead.
			bad := m
			bad.ID = "gpt-nonexistent-model"
			return Request{Model: bad, Messages: []Message{UserText("hi")}, MaxTokens: 256}
		},
		Extra: map[string]func(*testing.T, []map[string]any){
			"thinking": func(t *testing.T, bodies []map[string]any) {
				// R14: this protocol takes the effort level verbatim.
				if got := anthGet(t, bodies[0], "reasoning", "effort"); got != "high" {
					t.Errorf("reasoning.effort = %v, want high", got)
				}
				// Without these two, the reasoning item comes back with
				// nothing replayable and thinking_replay cannot work.
				if got := bodies[0]["store"]; got != false {
					t.Errorf("store = %v, want false", got)
				}
				var found bool
				for _, inc := range casList(bodies[0]["include"]) {
					if casStr(inc) == "reasoning.encrypted_content" {
						found = true
					}
				}
				if !found {
					t.Errorf("include = %v, want reasoning.encrypted_content", bodies[0]["include"])
				}
			},
		},
	})
}

// oairDecodeCassette reads a /responses body. Input is a flat item list, not
// a message list: reasoning, function_call and function_call_output are
// siblings of messages, so each item becomes its own normalized message.
func oairDecodeCassette(t *testing.T, _ string, body map[string]any) casView {
	v := casView{
		Model:     casStr(body["model"]),
		MaxTokens: casInt(body["max_output_tokens"]),
		Stream:    casBool(body["stream"]),
		System:    casStr(body["instructions"]),
	}
	_, v.HasTemp = body["temperature"]

	if r := casMap(body["reasoning"]); r != nil {
		v.Thinking = &casThinking{Enabled: true, Effort: casStr(r["effort"])}
	}
	for _, raw := range casList(body["tools"]) {
		td := casMap(raw)
		v.Tools = append(v.Tools, casTool{
			Name:        casStr(td["name"]),
			Description: casStr(td["description"]),
			Schema:      casMap(td["parameters"]),
		})
	}

	for _, raw := range casList(body["input"]) {
		item := casMap(raw)
		switch casStr(item["type"]) {
		case "message":
			msg := casMessage{Role: casStr(item["role"])}
			for _, rawp := range casList(item["content"]) {
				part := casMap(rawp)
				switch casStr(part["type"]) {
				case "input_text", "output_text":
					msg.Text += casStr(part["text"])
				case "input_image":
					msg.Images = append(msg.Images, casDataURI(casStr(part["image_url"])))
				case "input_file":
					f := casDataURI(casStr(part["file_data"]))
					msg.Documents = append(msg.Documents, casDocument{
						MimeType: f.MimeType, Data: f.Data, Name: casStr(part["filename"]),
					})
				}
			}
			v.Messages = append(v.Messages, msg)
		case "reasoning":
			// Signature round-trips as the encrypted content blob.
			v.Messages = append(v.Messages, casMessage{
				Role:     "assistant",
				Thinking: []casThinkingBlock{{Signature: casStr(item["encrypted_content"])}},
			})
		case "function_call":
			v.Messages = append(v.Messages, casMessage{
				Role: "assistant",
				ToolCalls: []casToolCall{{
					ID: casStr(item["call_id"]), Name: casStr(item["name"]),
					Args: casStr(item["arguments"]),
				}},
			})
		case "function_call_output":
			v.Messages = append(v.Messages, casMessage{
				Role: "user",
				ToolResults: []casToolResult{{
					ID: casStr(item["call_id"]), Text: casStr(item["output"]),
				}},
			})
		}
	}
	return v
}
