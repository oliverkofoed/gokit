package llm

// The anthropic-messages column of the cassette matrix (SPEC §12.2). The
// scenarios live in cassette_suite_test.go; this file is the descriptor and
// the wire decoder.
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run TestCassetteAnthropic -count=1

import (
	"testing"
)

func TestCassetteAnthropic(t *testing.T) {
	runCassetteSuite(t, casProvider{
		Name:       "anthropic",
		EnvKey:     "ANTHROPIC_API_KEY",
		Model:      ClaudeHaiku45,
		CacheModel: ClaudeHaiku45, // needs a >=4096-token prefix; casLongSystem is sized for it
		Foreign:    oairLunaModel,
		Temp:       casTemp(0),
		Decode:     anthDecodeCassette,
		BadRequest: func(m Model) Request {
			// Above the model ceiling: a validation error, so recording it
			// costs nothing and still returns the real error envelope.
			return Request{Model: m, Messages: []Message{UserText("hi")}, MaxTokens: 999_999}
		},
		Extra: map[string]func(*testing.T, []map[string]any){
			"basic_text": func(t *testing.T, bodies []map[string]any) {
				// R15: automatic breakpoints on the system block and on the
				// last block of the final message.
				if got := anthGet(t, bodies[0], "system", 0, "cache_control", "type"); got != "ephemeral" {
					t.Errorf("system cache_control = %v", got)
				}
				if got := anthGet(t, bodies[0], "messages", 0, "content", 0, "cache_control", "type"); got != "ephemeral" {
					t.Errorf("last message cache_control = %v", got)
				}
			},
			"tool_call": func(t *testing.T, bodies []map[string]any) {
				// R15: the breakpoint goes on the last tool definition only.
				if got := anthGet(t, bodies[0], "tools", 0, "cache_control", "type"); got != "ephemeral" {
					t.Errorf("last tool def cache_control = %v", got)
				}
			},
			"thinking": func(t *testing.T, bodies []map[string]any) {
				// R14: effort maps to a token budget, and temperature is
				// dropped because anthropic rejects both together.
				if got := anthGet(t, bodies[0], "thinking", "budget_tokens"); got != float64(2048) {
					t.Errorf("thinking.budget_tokens = %v, want 2048 for EffortLow", got)
				}
				if _, has := bodies[0]["temperature"]; has {
					t.Errorf("temperature must be omitted when thinking is enabled: %v", bodies[0]["temperature"])
				}
			},
			"caching": func(t *testing.T, bodies []map[string]any) {
				for i := range bodies {
					if got := anthGet(t, bodies[i], "system", 0, "cache_control", "type"); got != "ephemeral" {
						t.Errorf("request %d: system cache_control = %v", i, got)
					}
				}
			},
		},
	})
}

// anthDecodeCassette reads an anthropic-messages body. The field names it
// reaches for — system[], input_schema, tool_use/tool_result, source.data —
// are the wire-shape assertion: a body shaped like any other protocol would
// decode to nothing and fail the scenario.
func anthDecodeCassette(t *testing.T, _ string, body map[string]any) casView {
	v := casView{
		Model:     casStr(body["model"]),
		MaxTokens: casInt(body["max_tokens"]),
		Stream:    casBool(body["stream"]),
	}
	_, v.HasTemp = body["temperature"]

	for _, s := range casList(body["system"]) {
		v.System += casStr(casMap(s)["text"])
	}
	if th := casMap(body["thinking"]); th != nil {
		v.Thinking = &casThinking{
			Enabled: casStr(th["type"]) == "enabled",
			Budget:  casInt(th["budget_tokens"]),
		}
	}
	for _, raw := range casList(body["tools"]) {
		td := casMap(raw)
		v.Tools = append(v.Tools, casTool{
			Name:        casStr(td["name"]),
			Description: casStr(td["description"]),
			Schema:      casMap(td["input_schema"]),
		})
	}

	for _, raw := range casList(body["messages"]) {
		mm := casMap(raw)
		msg := casMessage{Role: casStr(mm["role"])}
		for _, rawb := range casList(mm["content"]) {
			b := casMap(rawb)
			switch casStr(b["type"]) {
			case "text":
				msg.Text += casStr(b["text"])
			case "thinking":
				msg.Thinking = append(msg.Thinking, casThinkingBlock{
					Text: casStr(b["thinking"]), Signature: casStr(b["signature"]),
				})
			case "redacted_thinking":
				msg.Thinking = append(msg.Thinking, casThinkingBlock{Signature: casStr(b["data"])})
			case "image":
				src := casMap(b["source"])
				msg.Images = append(msg.Images, casImage{
					MimeType: casStr(src["media_type"]), Data: casStr(src["data"]),
				})
			case "document":
				src := casMap(b["source"])
				msg.Documents = append(msg.Documents, casDocument{
					MimeType: casStr(src["media_type"]), Data: casStr(src["data"]),
					Name: casStr(b["title"]),
				})
			case "tool_use":
				msg.ToolCalls = append(msg.ToolCalls, casToolCall{
					ID: casStr(b["id"]), Name: casStr(b["name"]),
					Args: string(mustCasJSON(t, b["input"])),
				})
			case "tool_result":
				r := casToolResult{ID: casStr(b["tool_use_id"])}
				// Anthropic nests result content, images included (§8.2).
				for _, rawn := range casList(b["content"]) {
					n := casMap(rawn)
					switch casStr(n["type"]) {
					case "text":
						r.Text += casStr(n["text"])
					case "image":
						src := casMap(n["source"])
						r.Images = append(r.Images, casImage{
							MimeType: casStr(src["media_type"]), Data: casStr(src["data"]),
						})
					}
				}
				msg.ToolResults = append(msg.ToolResults, r)
			}
		}
		v.Messages = append(v.Messages, msg)
	}
	return v
}
