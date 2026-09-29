package llm

// The anthropic-messages column of the cassette matrix (SPEC §12.2). The
// scenarios live in cassette_suite_test.go; this file is the descriptor and
// the wire decoder.
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run TestCassetteAnthropic -count=1

import (
	"context"
	"strings"
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

// TestCassetteAnthropicAdaptive runs the matrix against a model that takes
// adaptive thinking and rejects sampling parameters, pinning that the API
// accepts the adaptive request shape.
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run TestCassetteAnthropicAdaptive -count=1
func TestCassetteAnthropicAdaptive(t *testing.T) {
	runCassetteSuite(t, anthAdaptiveProvider("anthropic_adaptive", ClaudeSonnet5))
}

// TestCassetteAnthropicFable runs the matrix against Fable, whose thinking is
// always on and whose API rejects explicit thinking configuration other than
// adaptive.
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run 'TestCassetteAnthropicFable' -count=1
func TestCassetteAnthropicFable(t *testing.T) {
	p := anthAdaptiveProvider("anthropic_fable", ClaudeFable51)
	p.Skip = map[string]string{
		// Fable answers the suite's arithmetic without thinking at any
		// effort (thinking_tokens: 0), so there is no signature to check.
		// The anthropic_adaptive column covers these, and
		// TestCassetteAnthropicAlwaysThinkingToolRoundtrip covers replay.
		"thinking":        "skips thinking on the suite's easy prompt",
		"thinking_replay": "skips thinking on the suite's easy prompt",
		// Fable's safety classifier refuses the suite's one-line PDF
		// ("general_harms", reproducibly as of 2026-09-28) — a false
		// positive. The request itself is accepted (HTTP 200); the
		// anthropic_adaptive column covers the document wire shape.
		"document_input": "safety classifier refuses the test PDF",
	}
	p.Extra["tool_result_roundtrip"] = func(t *testing.T, bodies []map[string]any) {
		anthNoTemp(t, bodies)
		for i, b := range bodies {
			if _, has := b["thinking"]; has {
				t.Errorf("request %d: thinking must be omitted when no reasoning is requested: %v", i, b["thinking"])
			}
		}
	}
	runCassetteSuite(t, p)
}

// TestCassetteAnthropicAlwaysThinkingToolRoundtrip is the live check that a
// thinking block with empty text (the default display on this generation)
// replays next to a tool call and its result. The prompt is a puzzle so the
// model reasons before calling the tool; the suite's prompts are too easy
// for Fable to think about at all. It runs for each model that always
// thinks: Fable 5.1, Opus 5.5 and Sonnet 5.5.
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run TestCassetteAnthropicAlwaysThinkingToolRoundtrip -count=1
func TestCassetteAnthropicAlwaysThinkingToolRoundtrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model Model
	}{
		{"anthropic_fable", ClaudeFable51},
		{"anthropic_opus55", ClaudeOpus55},
		{"anthropic_sonnet55", ClaudeSonnet55},
	} {
		t.Run(tc.model.ID, func(t *testing.T) {
			anthThinkingToolRoundtrip(t, anthAdaptiveProvider(tc.name, tc.model))
		})
	}
}

func anthThinkingToolRoundtrip(t *testing.T, p casProvider) {
	c, tee := p.client(t, "thinking_tool_roundtrip")
	ctx := context.Background()

	req := Request{
		Model:  p.Model,
		System: casToolSystem,
		Messages: []Message{UserText("Anna, Ben, Cleo, Dan and Eva each live in a different one of Oslo, Lisbon, " +
			"Vienna, Dublin and Prague. Anna does not live in Oslo, Lisbon, Dublin or Prague. Dan lives in " +
			"Prague or Vienna. Ben's city has the fewest letters of the five. Cleo's and Eva's cities both end " +
			"in the letter n, and Eva's does not start with D. Work out where Cleo lives, then get the " +
			"current weather there.")},
		Tools:     []ToolDef{casWeather},
		Reasoning: EffortHigh,
		MaxTokens: p.tokens(8192),
	}
	first, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if first.StopReason != StopToolUse {
		t.Fatalf("first turn stop = %q, want %q", first.StopReason, StopToolUse)
	}
	if first.Blocks[0].Type != BlockThinking || first.Blocks[0].Signature == "" {
		t.Fatalf("first turn did not think before the tool call: %+v", first.Blocks)
	}
	call := casFirstToolCall(t, first)
	if !strings.Contains(string(call.Args), "Dublin") {
		t.Errorf("tool args = %s, want Dublin", call.Args)
	}

	req.Messages = append(req.Messages, first,
		ToolResultMessage(call.ID, call.Name, false, TextBlock(casWeatherOut)))
	if _, err := c.Complete(ctx, req); err != nil {
		t.Fatalf("second turn (thinking replayed with the tool call): %v", err)
	}

	bodies := tee.bodies(t)
	block := casMap(anthGet(t, bodies[1], "messages", 1, "content", 0))
	if block["type"] != "thinking" {
		t.Fatalf("replayed assistant turn does not start with a thinking block: %v", block)
	}
	if _, has := block["thinking"]; !has {
		t.Errorf("replayed thinking block has no thinking field: %v", block)
	}
	if block["signature"] != first.Blocks[0].Signature {
		t.Errorf("replayed signature differs from the one returned")
	}
}

func anthNoTemp(t *testing.T, bodies []map[string]any) {
	for i, b := range bodies {
		if _, has := b["temperature"]; has {
			t.Errorf("request %d: temperature must be omitted on a no-sampling model: %v", i, b["temperature"])
		}
	}
}

// anthAdaptiveProvider describes a cassette column for an adaptive-thinking,
// no-sampling model.
func anthAdaptiveProvider(name string, m Model) casProvider {
	return casProvider{
		Name:       name,
		EnvKey:     "ANTHROPIC_API_KEY",
		Model:      m,
		CacheModel: m,
		Foreign:    oairLunaModel,
		Temp:       casTemp(0), // sent by every scenario, dropped by the quirk
		// Thinking text is omitted by default on this generation; the
		// signature still comes back and is what replay needs.
		NoThinkingText: true,
		// Adaptive thinking may skip reasoning on an easy prompt at low
		// effort, leaving no signature for the replay scenario.
		ThinkingEffort: EffortHigh,
		Decode:         anthDecodeCassette,
		BadRequest: func(m Model) Request {
			return Request{Model: m, Messages: []Message{UserText("hi")}, MaxTokens: 999_999}
		},
		Extra: map[string]func(*testing.T, []map[string]any){
			"basic_text": anthNoTemp,
			"tool_call":  anthNoTemp,
			"thinking": func(t *testing.T, bodies []map[string]any) {
				anthNoTemp(t, bodies)
				if got := anthGet(t, bodies[0], "thinking", "type"); got != "adaptive" {
					t.Errorf("thinking.type = %v, want adaptive", got)
				}
				if _, has := casMap(bodies[0]["thinking"])["budget_tokens"]; has {
					t.Errorf("budget_tokens must be omitted for adaptive thinking")
				}
				if got := anthGet(t, bodies[0], "output_config", "effort"); got != "high" {
					t.Errorf("output_config.effort = %v, want high", got)
				}
			},
		},
	}
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
			Enabled: casStr(th["type"]) == "enabled" || casStr(th["type"]) == "adaptive",
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
