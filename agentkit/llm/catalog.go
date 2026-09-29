package llm

// A small hand-written shorthand catalog (R2). This is intentionally a short
// list of well-known entries, not a generated one: models are config, and anything
// not listed here is a Model literal away. Prices are USD per million tokens
// as published at the time of writing — verify before relying on cost math
// for billing.

// ClaudeOpus55 always thinks; with no Request.Reasoning it runs at the
// API's default effort, which is medium on this model.
var ClaudeOpus55 = Model{
	ID: "claude-opus-5-5", Name: "Claude Opus 5.5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 4, Output: 20, CacheRead: 0.20, CacheWrite: 5},
	ContextWindow: 1_000_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{AnthropicAdaptiveThinking: true, AnthropicNoSampling: true},
}

var ClaudeOpus5 = Model{
	ID: "claude-opus-5", Name: "Claude Opus 5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25},
	ContextWindow: 1_000_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{AnthropicAdaptiveThinking: true, AnthropicNoSampling: true},
}

// ClaudeSonnet55 always thinks: it rejects thinking type "disabled", so with
// no Request.Reasoning it runs at the API's default effort.
var ClaudeSonnet55 = Model{
	ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 2.50},
	ContextWindow: 1_000_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{AnthropicAdaptiveThinking: true, AnthropicNoSampling: true},
}

var ClaudeSonnet5 = Model{
	ID: "claude-sonnet-5", Name: "Claude Sonnet 5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 2.50},
	ContextWindow: 1_000_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{AnthropicAdaptiveThinking: true, AnthropicNoSampling: true},
}

// ClaudeFable51 always thinks; with no Request.Reasoning it runs at the
// API's default effort.
var ClaudeFable51 = Model{
	ID: "claude-fable-5-1", Name: "Claude Fable 5.1",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.50},
	ContextWindow: 1_000_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{AnthropicAdaptiveThinking: true, AnthropicNoSampling: true},
}

var ClaudeSonnet45 = Model{
	ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3.75},
	ContextWindow: 200_000, MaxOutput: 64_000,
	Reasoning: true, Vision: true, Documents: true,
}

var ClaudeOpus46 = Model{
	ID: "claude-opus-4-6", Name: "Claude Opus 4.6",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25},
	ContextWindow: 1_000_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
}

var ClaudeHaiku45 = Model{
	ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 1, Output: 5, CacheRead: 0.10, CacheWrite: 1.25},
	ContextWindow: 200_000, MaxOutput: 64_000,
	Reasoning: true, Vision: true, Documents: true,
}

// GPT-6 prices are the standard tier for prompts up to 272K input tokens;
// longer prompts bill at a higher rate that Cost does not model. All three
// reason by default. Sol and Luna accept reasoning_effort "none", which
// openai-chat needs to send function tools to them; Astra rejects "none"
// and supports function tools only on the Responses API.

var GPT6Astra = Model{
	ID: "gpt-6-astra", Name: "GPT-6 Astra",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.50},
	ContextWindow: 1_050_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
}

var GPT6Sol = Model{
	ID: "gpt-6-sol", Name: "GPT-6 Sol",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 2.50},
	ContextWindow: 1_050_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{ReasoningEffortNone: true},
}

var GPT6Luna = Model{
	ID: "gpt-6-luna", Name: "GPT-6 Luna",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.125},
	ContextWindow: 1_050_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{ReasoningEffortNone: true},
}

var GPT5 = Model{
	ID: "gpt-5", Name: "GPT-5",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 1.25, Output: 10, CacheRead: 0.125},
	ContextWindow: 400_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
}

var GPT5Mini = Model{
	ID: "gpt-5-mini", Name: "GPT-5 Mini",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 0.25, Output: 2, CacheRead: 0.025},
	ContextWindow: 400_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true, Documents: true,
}

var Gemini25Pro = Model{
	ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 1.25, Output: 10, CacheRead: 0.125},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true, Documents: true,
}

var Gemini25Flash = Model{
	ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 0.30, Output: 2.50, CacheRead: 0.03},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true, Documents: true,
}

// Gemini38Flash prices apply through 2026-12-31; Google doubles them
// (1.50 / 7.50 / 0.15) from 2027-01-01. The same change applies to
// Gemini37Flash.
var Gemini38Flash = Model{
	ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 0.75, Output: 3.75, CacheRead: 0.075},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{GeminiV3: true},
}

var Gemini35FlashLite = Model{
	ID: "gemini-3.5-flash-lite", Name: "Gemini 3.5 Flash-Lite",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 0.30, Output: 2.50, CacheRead: 0.03},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{GeminiV3: true},
}

var Gemini37Flash = Model{
	ID: "gemini-3.7-flash", Name: "Gemini 3.7 Flash",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 0.75, Output: 3.75, CacheRead: 0.075},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true, Documents: true,
	Quirks: Quirks{GeminiV3: true},
}

// Catalog returns every Model defined in this file, as a fresh slice the
// caller may modify. OpenRouter models are built per id and not included.
func Catalog() []Model {
	return []Model{
		ClaudeFable51, ClaudeOpus55, ClaudeOpus5, ClaudeSonnet55, ClaudeSonnet5, ClaudeOpus46, ClaudeSonnet45, ClaudeHaiku45,
		GPT6Astra, GPT6Sol, GPT6Luna, GPT5, GPT5Mini,
		Gemini25Pro, Gemini25Flash, Gemini38Flash, Gemini37Flash, Gemini35FlashLite,
	}
}

// OpenRouter builds a Model for any model id on OpenRouter, speaking the
// openai-chat protocol. Pricing and limits are left zero — fill them in if
// you need cost tracking or overflow headroom.
func OpenRouter(modelID string) Model {
	return Model{
		ID:  modelID,
		API: OpenAIChat, Provider: "openrouter",
		BaseURL: "https://openrouter.ai/api/v1",
	}
}
