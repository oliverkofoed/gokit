package llm

// API identifies the wire protocol a model speaks. It selects the protocol
// implementation; everything else about a Model is plain data (R1).
type API string

const (
	AnthropicMessages API = "anthropic-messages"
	OpenAIResponses   API = "openai-responses"
	OpenAIChat        API = "openai-chat" // OpenAI Chat Completions and all compatibles
	GoogleGemini      API = "google-gemini"
)

// Cost is USD per million tokens.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// Quirks tunes protocol encoding for endpoint and model-generation variants.
// Most fields tune the openai-chat protocol for compatible endpoints; the
// zero value means "standard behavior" for whichever protocol the model uses.
type Quirks struct {
	// MaxTokensField overrides the field name for the output limit.
	// "" means "max_completion_tokens"; set "max_tokens" for older endpoints.
	MaxTokensField string `json:"max_tokens_field,omitempty"`
	// NoStreamUsage disables `stream_options: {"include_usage": true}`.
	NoStreamUsage bool `json:"no_stream_usage,omitempty"`
	// NoReasoningEffort omits the reasoning_effort parameter even when
	// Request.Reasoning is set (for servers that reject unknown fields).
	NoReasoningEffort bool `json:"no_reasoning_effort,omitempty"`
	// ReasoningEffortNone sends reasoning_effort:"none" when no reasoning is
	// requested, instead of omitting the field. Newer models reason by
	// default, and some reject function tools unless reasoning is explicitly
	// switched off — gpt-5.6-luna on /v1/chat/completions is one. Off by
	// default: older and compatible endpoints reject the "none" value.
	ReasoningEffortNone bool `json:"reasoning_effort_none,omitempty"`

	// AnthropicCacheControl emits Anthropic-style cache_control breakpoints
	// (supported by OpenRouter when routing to Anthropic models).
	AnthropicCacheControl bool `json:"anthropic_cache_control,omitempty"`

	// GeminiV3 selects Gemini 3.x request encoding: `thinking_level` and
	// `thinking_summaries` in place of `thinkingConfig`, and no sampling
	// parameters — that generation deprecated temperature/top_p/top_k,
	// which are ignored today and documented to become errors (R14).
	GeminiV3 bool `json:"gemini_v3,omitempty"`
}

// Model is plain, JSON-serializable data (R1). It has no methods; all
// behavior lives in the protocol implementation selected by API.
type Model struct {
	ID            string            `json:"id"`             // wire model id, e.g. "claude-sonnet-4-5"
	Name          string            `json:"name,omitempty"` // optional display name
	API           API               `json:"api"`
	Provider      string            `json:"provider"` // credential key, e.g. "anthropic", "openrouter"
	BaseURL       string            `json:"base_url"` // e.g. "https://api.anthropic.com"
	Cost          Cost              `json:"cost"`
	ContextWindow int               `json:"context_window"` // tokens
	MaxOutput     int               `json:"max_output"`     // tokens; used when Request.MaxTokens == 0
	Reasoning     bool              `json:"reasoning"`      // supports thinking/reasoning
	Vision        bool              `json:"vision"`         // accepts image input
	Documents     bool              `json:"documents"`      // accepts document (PDF) input
	Headers       map[string]string `json:"headers,omitempty"`
	Quirks        Quirks            `json:"quirks,omitempty"`
}
