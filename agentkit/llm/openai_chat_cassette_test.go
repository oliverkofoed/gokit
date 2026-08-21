package llm

// The openai-chat column of the cassette matrix (SPEC §12.2). Scenarios live
// in cassette_suite_test.go.
//
//	OPENAI_API_KEY=sk-... RECORD=1 go test ./llm/ -run TestCassetteOpenAIChat -count=1

import (
	"testing"
)

// oaicCassetteModel drives the shared /chat/completions protocol against
// OpenAI. The same descriptor with a different BaseURL and Quirks is how an
// OpenRouter, Groq, or Ollama column would be added.
var oaicCassetteModel = Model{
	ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna (chat completions)",
	API: OpenAIChat, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 0.20, Output: 1.20, CacheRead: 0.02},
	ContextWindow: 1_050_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true,
	// Luna reasons by default, and /v1/chat/completions rejects function
	// tools unless reasoning is explicitly off: "Function tools with
	// reasoning_effort are not supported for gpt-5.6-luna ... set
	// reasoning_effort to 'none'."
	Quirks: Quirks{ReasoningEffortNone: true},
}

func TestCassetteOpenAIChat(t *testing.T) {
	runCassetteSuite(t, casProvider{
		Name:    "openai_chat",
		EnvKey:  "OPENAI_API_KEY",
		Model:   oaicCassetteModel,
		Foreign: ClaudeHaiku45,
		// Temp stays nil: the GPT-5 family rejects any temperature but 1.
		MinOutputTokens: 2048, // luna reasons by default; a small cap yields no text
		Decode:          oaicDecodeCassette,
		BadRequest: func(m Model) Request {
			return Request{Model: m, Messages: []Message{UserText("hi")}, MaxTokens: 99_999_999}
		},
		Skip: map[string]string{
			// Chat Completions accepts reasoning_effort but returns no
			// reasoning content, so there is no thinking block to assert and
			// nothing to replay. The request-side mapping is covered by the
			// scripted quirk tests in openai_chat_test.go.
			"thinking":        "chat completions returns no reasoning content",
			"thinking_replay": "chat completions returns no reasoning content",
		},
		Extra: map[string]func(*testing.T, []map[string]any){
			"basic_text": func(t *testing.T, bodies []map[string]any) {
				// The output limit uses the modern field name by default.
				if _, has := bodies[0]["max_completion_tokens"]; !has {
					t.Errorf("want max_completion_tokens, got keys %v", casKeys(bodies[0]))
				}
				if _, has := bodies[0]["max_tokens"]; has {
					t.Error("legacy max_tokens set without the MaxTokensField quirk")
				}
				// Usage only arrives on the stream when this is asked for.
				if got := anthGet(t, bodies[0], "stream_options", "include_usage"); got != true {
					t.Errorf("stream_options.include_usage = %v", got)
				}
			},
		},
	})
}

func casKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// oaicDecodeCassette reads a /chat/completions body. The system prompt is a
// leading message rather than a dedicated field, so it is lifted into
// casView.System and not emitted as a conversation message.
func oaicDecodeCassette(t *testing.T, _ string, body map[string]any) casView {
	v := casView{
		Model:  casStr(body["model"]),
		Stream: casBool(body["stream"]),
	}
	if mt, has := body["max_completion_tokens"]; has {
		v.MaxTokens = casInt(mt)
	} else {
		v.MaxTokens = casInt(body["max_tokens"]) // MaxTokensField quirk
	}
	_, v.HasTemp = body["temperature"]
	if eff, has := body["reasoning_effort"]; has {
		v.Thinking = &casThinking{Enabled: true, Effort: casStr(eff)}
	}

	for _, raw := range casList(body["tools"]) {
		fn := casMap(casMap(raw)["function"])
		v.Tools = append(v.Tools, casTool{
			Name:        casStr(fn["name"]),
			Description: casStr(fn["description"]),
			Schema:      casMap(fn["parameters"]),
		})
	}

	for _, raw := range casList(body["messages"]) {
		mm := casMap(raw)
		role := casStr(mm["role"])
		if role == "system" {
			v.System += casStr(mm["content"])
			continue
		}
		msg := casMessage{Role: role}
		if role == "tool" {
			// role:tool carries text only; images spilled into a following
			// user message (§8.2), which this loop picks up separately.
			msg.Role = "user"
			msg.ToolResults = []casToolResult{{
				ID: casStr(mm["tool_call_id"]), Text: casStr(mm["content"]),
			}}
			v.Messages = append(v.Messages, msg)
			continue
		}
		switch content := mm["content"].(type) {
		case string:
			msg.Text = content
		case []any:
			for _, rawp := range content {
				part := casMap(rawp)
				switch casStr(part["type"]) {
				case "text":
					msg.Text += casStr(part["text"])
				case "image_url":
					msg.Images = append(msg.Images, casDataURI(casStr(casMap(part["image_url"])["url"])))
				}
			}
		}
		for _, rawc := range casList(mm["tool_calls"]) {
			call := casMap(rawc)
			fn := casMap(call["function"])
			msg.ToolCalls = append(msg.ToolCalls, casToolCall{
				ID: casStr(call["id"]), Name: casStr(fn["name"]), Args: casStr(fn["arguments"]),
			})
		}
		v.Messages = append(v.Messages, msg)
	}
	return v
}
