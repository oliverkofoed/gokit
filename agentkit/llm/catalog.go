package llm

// A small hand-written shorthand catalog (R2). This is intentionally a dozen
// well-known entries, not a generated list: models are config, and anything
// not listed here is a Model literal away. Prices are USD per million tokens
// as published at the time of writing — verify before relying on cost math
// for billing.

var ClaudeSonnet45 = Model{
	ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3.75},
	ContextWindow: 200_000, MaxOutput: 64_000,
	Reasoning: true, Vision: true,
}

var ClaudeOpus46 = Model{
	ID: "claude-opus-4-6", Name: "Claude Opus 4.6",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25},
	ContextWindow: 200_000, MaxOutput: 64_000,
	Reasoning: true, Vision: true,
}

var ClaudeHaiku45 = Model{
	ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5",
	API: AnthropicMessages, Provider: "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Cost:          Cost{Input: 1, Output: 5, CacheRead: 0.10, CacheWrite: 1.25},
	ContextWindow: 200_000, MaxOutput: 64_000,
	Reasoning: true, Vision: true,
}

var GPT5 = Model{
	ID: "gpt-5", Name: "GPT-5",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 1.25, Output: 10, CacheRead: 0.125},
	ContextWindow: 400_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true,
}

var GPT5Mini = Model{
	ID: "gpt-5-mini", Name: "GPT-5 Mini",
	API: OpenAIResponses, Provider: "openai",
	BaseURL:       "https://api.openai.com/v1",
	Cost:          Cost{Input: 0.25, Output: 2, CacheRead: 0.025},
	ContextWindow: 400_000, MaxOutput: 128_000,
	Reasoning: true, Vision: true,
}

var Gemini25Pro = Model{
	ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 1.25, Output: 10, CacheRead: 0.31},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true,
}

var Gemini25Flash = Model{
	ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 0.30, Output: 2.50, CacheRead: 0.075},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true,
}

var Gemini37Flash = Model{
	ID: "gemini-3.7-flash", Name: "Gemini 3.7 Flash",
	API: GoogleGemini, Provider: "google",
	BaseURL:       "https://generativelanguage.googleapis.com",
	Cost:          Cost{Input: 0.75, Output: 3.75, CacheRead: 0.075},
	ContextWindow: 1_048_576, MaxOutput: 65_536,
	Reasoning: true, Vision: true,
	Quirks: Quirks{GeminiV3: true},
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
