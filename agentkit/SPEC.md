# Agentkit Specification

Agentkit is a Go library for building agent loops — especially coding agents — on top of LLM providers. It is inspired by `pi` (`@earendil-works/pi-ai` and `@earendil-works/pi-agent-core`)

**Module path:** `github.com/oliverkofoed/gokit/agentkit`
**Go version:** set by the `gokit` module (agentkit itself needs 1.23+ — it uses `iter.Seq` range-over-func)
**Dependencies:** standard library only. No provider SDKs, no third-party HTTP, no schema libraries.

This document is the complete build spec. Every public type and function is given with its exact signature. Behavior rules are numbered (`R1`, `R2`, …) so tests can reference them. A competent implementer should be able to build the library from this document plus the providers' public API documentation; recorded cassettes provide wire-level ground truth.

---

## Table of Contents

1. [Design Principles](#1-design-principles)
2. [Package Layout](#2-package-layout)
3. [The `llm` Package: Types](#3-the-llm-package-types)
4. [The `llm` Package: Client and Streams](#4-the-llm-package-client-and-streams)
5. [Reasoning, Caching, and Cross-Model Handoff](#5-reasoning-caching-and-cross-model-handoff)
6. [The `transport` Package](#6-the-transport-package)
7. [The `cassette` Package](#7-the-cassette-package)
8. [Wire Protocols](#8-wire-protocols)
9. [The `llmtest` Package: Scripted Fake](#9-the-llmtest-package-scripted-fake)
10. [The `agentkit` Package: Tools](#10-the-agentkit-package-tools)
11. [The `agentkit` Package: Session](#11-the-agentkit-package-session)
12. [Test Suite](#12-test-suite)
13. [Implementation Milestones](#13-implementation-milestones)
14. [Non-Goals](#14-non-goals)

---

## 1. Design Principles

**P1 — Few primitives.** The entire public surface is seven concepts: `Model`, `Message`, `Request`, `Stream`, `Transport`, `Tool`, `Session`. Everything else is a field, an option, or a helper.

**P2 — Models are data.** A `Model` is a plain serializable struct. All protocol behavior is selected by `Model.API` and lives in one file per protocol inside `llm`. Users construct models as literals, load them from JSON config, or use the small hand-written shorthand catalog. There is no code generation.

**P3 — One hole in the wall.** Every byte that leaves the process goes through `transport.Interface`. Providers never call `net/http` directly. This is what makes cassette recording total: replace the transport, and the whole library — all four protocols, retries, SSE parsing — runs against recorded traffic.

**P4 — Pull, don't push.** Streams and session runs are iterators (`iter.Seq[Event]`). The agent loop executes inside the iterator as the consumer pulls. No background goroutines except parallel tool execution. No subscription lifecycle, no channel draining obligations.

**P5 — Errors are values, twice.** Hard failures return Go errors. Failures _mid-stream_ (after a request started) are delivered as a terminal `error` event and a final `Message` with `StopReason: StopError` and partial content — the loop can persist, display, and continue from them. Convenience methods return both: the partial message _and_ an error.

**P6 — Tagged structs over interfaces.** `Message`, `Block`, and `Event` are single structs with a `Type`/`Role` discriminator field, not interface hierarchies. They marshal to JSON directly, switch cleanly, and are easy to construct in tests.

---

## 2. Package Layout

```
github.com/oliverkofoed/gokit/agentkit
├── session.go, tool.go, event.go, schema.go   # package agentkit (the agent loop)
├── llm/                                        # package llm (stateless model calls)
│   ├── model.go, message.go, request.go, catalog.go
│   ├── client.go, stream.go, auth.go, errors.go, partial_json.go
│   ├── convert.go                              # history normalization, handoff rules
│   ├── anthropic.go                            # anthropic-messages protocol
│   ├── openai_chat.go                          # openai chat-completions protocol
│   ├── openai_responses.go                     # openai responses protocol
│   ├── gemini.go                               # google gemini protocol
│   ├── transport/                              # package transport (the only network seam)
│   │   └── transport.go
│   ├── cassette/                               # package cassette (record/replay transport)
│   │   └── cassette.go
│   ├── llmtest/                                # package llmtest (scripted fake Streamer)
│   │   └── fake.go
│   ├── internal/
│   │   └── sse/                                # shared SSE reader
│   └── testdata/cassettes/                     # committed provider traffic
└── examples/
    ├── chat/main.go                            # minimal streaming chat
    ├── codingagent/main.go                     # tools + session loop
    └── switchmodel/main.go                     # mid-session model switch + steering
```

The four protocol files are part of package `llm`, not separate internal packages: each is a method on `*Client` written in `llm`'s own types (§8). Retry lives in `client.go`; the cassette file format lives in `cassette.go`.

Import graph (arrows = imports): `agentkit → llm → transport`; `llm → internal/sse`; `cassette → transport`; `llmtest → llm`. Nothing imports upward.

---

## 3. The `llm` Package: Types

### 3.1 Model

```go
package llm

// API identifies the wire protocol a model speaks.
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

// Quirks tunes the openai-chat protocol for compatible endpoints.
// The zero value means "standard OpenAI behavior".
type Quirks struct {
    // MaxTokensField overrides the field name for the output limit.
    // "" means "max_completion_tokens"; set "max_tokens" for older endpoints.
    MaxTokensField string `json:"max_tokens_field,omitempty"`
    // NoStreamUsage disables `stream_options: {"include_usage": true}`.
    NoStreamUsage bool `json:"no_stream_usage,omitempty"`
    // NoReasoningEffort omits the reasoning_effort parameter even when
    // Request.Reasoning is set (for servers that reject unknown fields).
    NoReasoningEffort bool `json:"no_reasoning_effort,omitempty"`
    // AnthropicCacheControl emits Anthropic-style cache_control breakpoints
    // (supported by OpenRouter when routing to Anthropic models).
    AnthropicCacheControl bool `json:"anthropic_cache_control,omitempty"`
}

// Model is plain data. It has no methods beyond validation.
type Model struct {
    ID            string            `json:"id"`             // wire model id, e.g. "claude-sonnet-4-5"
    API           API               `json:"api"`
    Provider      string            `json:"provider"`       // credential key, e.g. "anthropic", "openrouter"
    BaseURL       string            `json:"base_url"`       // e.g. "https://api.anthropic.com"
    Cost          Cost              `json:"cost"`
    ContextWindow int               `json:"context_window"` // tokens
    MaxOutput     int               `json:"max_output"`     // tokens; used when Request.MaxTokens == 0
    Reasoning     bool              `json:"reasoning"`      // supports thinking/reasoning
    Vision        bool              `json:"vision"`         // accepts image input
    Documents     bool              `json:"documents"`      // accepts document (PDF) input
    Headers       map[string]string `json:"headers,omitempty"` // extra headers on every request
    Quirks        Quirks            `json:"quirks,omitempty"`
}
```

**R1** — `Model` must round-trip through `encoding/json` losslessly.

**R2** — A small hand-written shorthand catalog ships as package-level vars in `llm/catalog.go`, maintained by hand (a dozen entries, not hundreds). Today: `ClaudeSonnet45`, `ClaudeOpus46`, `ClaudeHaiku45`, `GPT5`, `GPT5Mini`, `Gemini25Pro`, `Gemini25Flash`, `Gemini37Flash`, plus the `OpenRouter` helper. Entries go stale; they are a convenience, not an API contract — construct a `Model` literal for anything the catalog does not cover.

```go
// llm/catalog.go — illustrative entries; keep this list short and current.
var ClaudeSonnet45 = Model{
    ID: "claude-sonnet-4-5", API: AnthropicMessages, Provider: "anthropic",
    BaseURL: "https://api.anthropic.com",
    Cost: Cost{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3.75},
    ContextWindow: 200_000, MaxOutput: 64_000, Reasoning: true, Vision: true,
}
var GPT5Mini = Model{
    ID: "gpt-5-mini", API: OpenAIResponses, Provider: "openai",
    BaseURL: "https://api.openai.com/v1", /* … */
}
var Gemini25Flash = Model{ /* … API: GoogleGemini … */ }

// OpenRouter helper: any model on OpenRouter via openai-chat.
func OpenRouter(modelID string) Model {
    return Model{
        ID: modelID, API: OpenAIChat, Provider: "openrouter",
        BaseURL: "https://openrouter.ai/api/v1",
    }
}
```

### 3.2 Messages and Blocks

```go
type Role string

const (
    RoleUser       Role = "user"
    RoleAssistant  Role = "assistant"
    RoleToolResult Role = "tool_result"
)

type StopReason string

const (
    StopEnd     StopReason = "stop"     // normal completion
    StopLength  StopReason = "length"   // output token limit
    StopToolUse StopReason = "tool_use" // model wants tool results
    StopError   StopReason = "error"    // request failed mid-flight
    StopAborted StopReason = "aborted"  // context cancelled / interrupted
)

type BlockType string

const (
    BlockText     BlockType = "text"
    BlockThinking BlockType = "thinking"
    BlockImage    BlockType = "image"
    BlockDocument BlockType = "document"
    BlockToolCall BlockType = "tool_call"
)

// Block is a tagged union. Only the fields for its Type are set.
type Block struct {
    Type BlockType `json:"type"`

    // BlockText, BlockThinking
    Text string `json:"text,omitempty"`
    // Signature carries opaque provider replay metadata:
    // Anthropic thinking signatures, OpenAI Responses reasoning
    // encrypted_content / item ids, Gemini thought signatures.
    // Preserve verbatim; never inspect.
    Signature string `json:"signature,omitempty"`
    // Redacted marks safety-redacted thinking (content lives in Signature).
    Redacted bool `json:"redacted,omitempty"`

    // BlockImage, BlockDocument
    Data     string `json:"data,omitempty"`      // base64, no data: prefix
    MimeType string `json:"mime_type,omitempty"` // "image/png", "application/pdf", ...

    // BlockToolCall; BlockDocument uses Name for the file name
    ID   string          `json:"id,omitempty"`
    Name string          `json:"name,omitempty"`
    Args json.RawMessage `json:"args,omitempty"` // decoded JSON object; "{}" while empty
}

// Usage counts tokens for one assistant message.
type Usage struct {
    Input      int     `json:"input"`
    Output     int     `json:"output"`
    CacheRead  int     `json:"cache_read"`
    CacheWrite int     `json:"cache_write"`
    TotalCost  float64 `json:"total_cost"` // USD, computed from Model.Cost
}

type Message struct {
    Role   Role    `json:"role"`
    Blocks []Block `json:"blocks"`
    Time   time.Time `json:"time"`

    // Kind marks an app-level message (UI notification, divider, compaction
    // marker, audit note). Empty = a normal LLM message. Non-empty-Kind
    // messages live in history and State JSON but are never sent to the
    // model (R37). Role and Blocks may be empty on Kind messages.
    Kind string `json:"kind,omitempty"`
    // Meta carries app data for Kind messages; opaque to the library.
    Meta json.RawMessage `json:"meta,omitempty"`

    // RoleToolResult only:
    ToolCallID string `json:"tool_call_id,omitempty"`
    ToolName   string `json:"tool_name,omitempty"`
    IsError    bool   `json:"is_error,omitempty"`

    // RoleAssistant only (response metadata):
    Model      string     `json:"model,omitempty"`    // Model.ID that produced it
    Provider   string     `json:"provider,omitempty"` // Model.Provider
    API        API        `json:"api,omitempty"`      // Model.API
    StopReason StopReason `json:"stop_reason,omitempty"`
    ErrorText  string     `json:"error_text,omitempty"` // set when StopReason is error/aborted
    Usage      *Usage     `json:"usage,omitempty"`
}
```

Constructors (all trivial, all in `message.go`):

```go
func UserText(text string) Message
func UserBlocks(blocks ...Block) Message
func TextBlock(text string) Block
func ImageBlock(mimeType string, data []byte) Block // base64-encodes
func DocumentBlock(mimeType, name string, data []byte) Block // base64-encodes
func ToolResultMessage(callID, toolName string, isError bool, blocks ...Block) Message
func AppMessage(kind string, meta json.RawMessage) Message // Kind message, no Role
```

**R3** — Every constructor sets `Time` to `time.Now()`. Messages round-trip through JSON losslessly (`json.RawMessage` for `Args` guarantees byte preservation).

**R37 — App-level messages.** A message with non-empty `Kind` is inert to the LLM layer: history normalization (§5.3) removes it before any other rule runs, so it never influences a request payload, orphan pairing, or handoff logic. It is preserved verbatim in `Session.State()`, survives `Resume`, and is announced via `EventMessage` like any other append. `Kind` values are app-defined; the library never interprets them. (This replaces pi's custom-message-role declaration merging + `convertToLlm` filtering with one field and one filter rule.)

**R4** — `Usage.TotalCost = (Input·Cost.Input + Output·Cost.Output + CacheRead·Cost.CacheRead + CacheWrite·Cost.CacheWrite) / 1e6`. A helper `func ComputeCost(m Model, u Usage) float64` exposes the same math.

### 3.3 Request

```go
// ToolDef describes a tool to the model. Schema is standard JSON Schema
// (draft 2020-12 subset; see §8 for per-protocol sanitization).
type ToolDef struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Schema      json.RawMessage `json:"schema"`
}

// Effort is the uniform reasoning level, mapped per protocol (§5.1).
type Effort string

const (
    EffortOff    Effort = ""       // zero value: no reasoning requested
    EffortLow    Effort = "low"
    EffortMedium Effort = "medium"
    EffortHigh   Effort = "high"
)

type Request struct {
    Model    Model
    System   string
    Messages []Message
    Tools    []ToolDef

    Reasoning   Effort
    MaxTokens   int      // 0 → Model.MaxOutput
    Temperature *float64 // nil → provider default

    // DisableCache turns off automatic prompt-cache breakpoints (§5.2).
    // Zero value = caching enabled.
    DisableCache bool

    // APIKey overrides client-level auth for this request.
    APIKey string
}
```

**R5** — `Request` is a value; the client never mutates the caller's `Messages` slice or the `Model`. All history normalization (§5.3) happens on copies.

---

## 4. The `llm` Package: Client and Streams

### 4.1 Events

```go
type EventType string

const (
    EventStart         EventType = "start"          // always first
    EventTextStart     EventType = "text_start"
    EventTextDelta     EventType = "text_delta"
    EventTextEnd       EventType = "text_end"
    EventThinkingStart EventType = "thinking_start"
    EventThinkingDelta EventType = "thinking_delta"
    EventThinkingEnd   EventType = "thinking_end"
    EventToolCallStart EventType = "tool_call_start"
    EventToolCallDelta EventType = "tool_call_delta"
    EventToolCallEnd   EventType = "tool_call_end"
    EventDone          EventType = "done"           // terminal success
    EventError         EventType = "error"          // terminal failure (incl. abort)
)

type Event struct {
    Type  EventType
    Index int    // content block index this event pertains to
    Delta string // incremental text on *_delta (for tool_call_delta: raw JSON fragment)

    // Block is the completed block, set on text_end / thinking_end / tool_call_end.
    Block *Block

    // Message is the partial message so far, on every event; on EventDone and
    // EventError it is the final message. It is a live accumulator owned by the
    // stream — copy it if you retain it past the current iteration.
    Message *Message

    // Err is set on EventError. errors.Is(Err, context.Canceled) distinguishes
    // aborts; Message.StopReason is StopAborted vs StopError accordingly.
    Err error
}
```

**R6 — Event ordering contract.** A stream emits exactly: one `EventStart`; then zero or more block groups; then exactly one terminal event (`EventDone` or `EventError`). Each content block `i` produces `*_start(i)`, zero or more `*_delta(i)`, `*_end(i)`. Events for _different_ blocks may interleave (some providers stream text and tool calls in the same chunk); events for the _same_ block are always in start→delta→end order. Consumers must key on `Index`.

**R7 — Tool-call deltas.** During `tool_call_delta`, `Message.Blocks[Index].Args` contains a best-effort parse of the partial JSON (using an internal partial-JSON parser: parse the longest valid prefix, auto-closing open strings/arrays/objects). It is at minimum `{}`, never nil. At `tool_call_end`, `Args` is the complete argument JSON (still unvalidated against the tool schema — validation is the agent layer's job, §10.3).

**R8 — Terminal errors, not panics.** After `Stream` is returned to the caller, no failure may panic or be silently dropped. Transport errors, malformed SSE, HTTP error statuses, and context cancellation all produce `EventError` with a final `Message` carrying partial content, `StopReason` `StopError` or `StopAborted`, and `ErrorText`. HTTP error bodies are included verbatim in `ErrorText` (truncated to 2 KiB) — provider error bodies are the single most useful debugging artifact.

### 4.2 Stream

```go
// Stream is a single in-flight completion.
type Stream struct { /* unexported */ }

// Events iterates the stream. The request executes as the consumer pulls;
// breaking early cancels the request (the final state is then available
// via Message with StopReason StopAborted).
func (s *Stream) Events() iter.Seq[Event]

// Message drains any remaining events and returns the final assistant
// message. err is non-nil iff StopReason is StopError or StopAborted;
// the message is returned in both cases (partial content preserved).
func (s *Stream) Message() (Message, error)
```

**R9** — `Events()` may be ranged at most once; a second range yields nothing. `Message()` can be called before, during (from the same goroutine after breaking), or after iteration and always returns the same final result.

**R10** — Producer construction for fakes and internal protocol implementations:

```go
// NewStream runs produce in the calling goroutine of Events()/Message().
// produce must emit a valid event sequence per R6; emit returns false if
// the consumer stopped (produce should return promptly).
// NewStream wraps produce with accumulator bookkeeping: it maintains the
// partial Message, fills Event.Message on every event, synthesizes the
// final message on Done/Error, and guarantees exactly one terminal event
// (appending an EventError if produce returns without one or panics).
func NewStream(model Model, produce func(emit func(Event) bool)) *Stream
```

Internal protocol implementations emit _raw_ events (Type, Index, Delta, block fields) and let `NewStream` do all accumulation. This centralizes the partial-message logic in one tested place.

### 4.3 Client

```go
// Streamer is the minimal capability the agent layer depends on.
// *Client implements it; llmtest.Fake implements it.
type Streamer interface {
    Stream(ctx context.Context, req Request) *Stream
}

type Client struct { /* unexported */ }

type Option func(*Client)

func New(opts ...Option) *Client

// WithTransport replaces the network transport (default: transport.HTTP()).
func WithTransport(t transport.Interface) Option

// WithAPIKey sets a static key for a provider name (Model.Provider).
func WithAPIKey(provider, key string) Option

// WithKeyFunc resolves keys dynamically (vaults, expiring gateway tokens).
// Called once per request. Takes precedence below static keys (see R11).
func WithKeyFunc(fn func(ctx context.Context, provider string) (string, error)) Option

// WithRetry sets max retry attempts for retryable failures (default 2,
// exponential backoff 500ms·2^n + jitter, honoring Retry-After).
func WithRetry(maxRetries int) Option

// WithThinkingBudgets overrides token budgets for Anthropic/Gemini
// effort mapping (§5.1). Defaults: low 2048, medium 8192, high 24576.
func WithThinkingBudgets(budgets map[Effort]int) Option

func (c *Client) Stream(ctx context.Context, req Request) *Stream

// Complete = Stream + Message.
func (c *Client) Complete(ctx context.Context, req Request) (Message, error)
```

**R11 — Key resolution order.** For a request against `Model.Provider == p`: (1) `Request.APIKey` if non-empty; (2) `WithAPIKey(p, …)` static key; (3) `WithKeyFunc` result if configured (empty string + nil error means "not found, keep going"); (4) environment variable per this fixed table:

| Provider      | Env var                                                     |
| ------------- | ----------------------------------------------------------- |
| `anthropic`   | `ANTHROPIC_API_KEY`                                         |
| `openai`      | `OPENAI_API_KEY`                                            |
| `google`      | `GEMINI_API_KEY`                                            |
| `openrouter`  | `OPENROUTER_API_KEY`                                        |
| any other `p` | `strings.ToUpper(p) + "_API_KEY"` (non-alphanumerics → `_`) |

If no key resolves, the stream emits `EventError` with a typed error `ErrNoAPIKey` (wrapped, provider name in message). Keyless local servers (Ollama etc.): register `WithAPIKey("ollama", llm.NoAuth)`; that sentinel is the literal `-` and means "send no auth header".

**R12 — Retry.** Retryable: HTTP 408, 429, 5xx, and transport-level connection errors — but only if no content event (`text_start` etc.) has been emitted yet. Once content flows, failures are terminal (streams are not resumable). Retries are invisible to the consumer except for latency. `Retry-After` (seconds or HTTP date) caps at 60s.

**R13 — Concurrency.** `Client` is safe for concurrent use. A `Stream` is single-consumer.

Two more exported error values support the above: `ErrNoAPIKey` (R11) and `*HTTPError`, which carries a non-2xx `Status` and the provider's response body so callers can distinguish a 429 from a malformed request without string matching.

---

## 5. Reasoning, Caching, and Cross-Model Handoff

### 5.1 Uniform reasoning levels

**R14** — `Request.Reasoning` maps per protocol; on models with `Reasoning: false` it is silently ignored:

| Effort   | anthropic-messages (`thinking.budget_tokens`) | openai-responses (`reasoning.effort`) | openai-chat (`reasoning_effort`) | google-gemini ≤2.5 (`thinkingConfig.thinkingBudget`) | google-gemini 3.x (`thinking_level`) |
| -------- | --------------------------------------------- | ------------------------------------- | -------------------------------- | --------------------------------------------------- | ------------------------------------ |
| `""`     | omit `thinking`                               | omit                                  | omit                             | omit                                                | omit                                 |
| `low`    | 2048                                          | `"low"`                               | `"low"`                          | 2048                                                | `"low"`                              |
| `medium` | 8192                                          | `"medium"`                            | `"medium"`                       | 8192                                                | `"medium"`                           |
| `high`   | 24576                                         | `"high"`                              | `"high"`                         | 24576                                               | `"high"`                             |

On openai-chat, `Quirks.ReasoningEffortNone` sends `reasoning_effort: "none"` for `EffortOff` instead of omitting the field — newer models reason by default, and some reject function tools unless reasoning is explicitly disabled. It is opt-in because compatible endpoints reject the value.

Budgets are overridable via `WithThinkingBudgets`. Anthropic: when thinking is enabled, `temperature` must be omitted (API constraint) and `max_tokens` must exceed the budget — if `MaxTokens ≤ budget`, use `budget + 4096`. Gemini ≤2.5: also set `includeThoughts: true`. Gemini 3.x (`Quirks.GeminiV3`): the budget is replaced by the categorical `thinking_level`, thought summaries are opted into with `thinking_summaries: "auto"`, and the sampling parameters (`temperature`, `top_p`, `top_k`) are omitted entirely — that generation deprecated them, and they are documented to become errors.

### 5.2 Prompt caching

**R15** — Unless `DisableCache` is set:

- **anthropic-messages**: place `cache_control: {"type": "ephemeral"}` on (1) the last system block, (2) the last tool definition, (3) the last block of the final message. Never exceed 4 breakpoints.
- **openai-chat with `Quirks.AnthropicCacheControl`**: same breakpoints in OpenRouter's format.
- **openai-responses / openai-chat / gemini**: no explicit breakpoints (these providers cache implicitly); the flag is a no-op.

Cache token counts flow into `Usage.CacheRead`/`Usage.CacheWrite` wherever the provider reports them.

### 5.3 History normalization (`convert.go`)

Applied to a copy of `Request.Messages` before protocol encoding, in this order (step 0 first: remove all messages with non-empty `Kind`, per R37):

**R16 — Orphaned tool calls.** An assistant message containing `tool_call` blocks whose IDs are not answered by a following `tool_result` message gets a synthetic error tool result injected immediately after it: `IsError: true`, text `"tool call was interrupted; no result available"`. (This happens after aborts; every protocol requires call/result pairing.)

**R17 — Cross-model thinking handoff.** When an assistant message's `Model` differs from `Request.Model.ID`, each `thinking` block is converted to a `text` block wrapped as `<thinking>…</thinking>\n`, and its signature is dropped; redacted thinking blocks are dropped entirely. Same-model messages keep thinking blocks and signatures for native replay (protocols that cannot replay thinking drop them, per §8).

**R18 — Tool-call ID normalization.** Tool call IDs generated by other providers may violate the target's constraints. Sanitize IDs (both in `tool_call` blocks and matching `tool_result` messages, consistently) to `[a-zA-Z0-9_-]{1,40}`; replace invalid chars with `_`. Gemini historically returns empty IDs — synthesize `call_<n>` (index-based, deterministic) when empty.

**R19 — Empty content.** Assistant messages with zero blocks (possible after abort-before-first-token) are dropped from replay. User messages with empty text are sent as a single space (some providers reject empty content).

**R20 — Vision downgrade.** If `Model.Vision` is false, image blocks are replaced by the text `[image omitted]` rather than causing an error.

**R38 — Document downgrade.** If `Model.Documents` is false, document blocks are replaced by the text `[document <name> omitted]` (`[document omitted]` when the block carries no name) rather than causing an error. Documents are a user message's to send: every protocol encoder drops a document block placed in a tool result, since no provider accepts one in that position — text and images are all a tool result carries (§8.1–§8.4).

---

## 6. The `transport` Package

The complete package — small on purpose:

```go
package transport

// Request is a fully materialized HTTP request. Body is a byte slice,
// not a reader: requests must be replayable for retries and recordable
// for cassettes.
type Request struct {
    Method string
    URL    string
    Header http.Header
    Body   []byte
}

// Response is a streamed HTTP response. Body must be closed by the caller.
type Response struct {
    Status int
    Header http.Header
    Body   io.ReadCloser
}

// Interface is the only path bytes take out of the process.
type Interface interface {
    Do(ctx context.Context, req *Request) (*Response, error)
}

// HTTP returns the production transport backed by http.Client.
// No overall timeout (streams are long-lived); dial/TLS timeouts apply;
// respects HTTP_PROXY/HTTPS_PROXY via http.ProxyFromEnvironment.
func HTTP(opts ...HTTPOption) Interface

type HTTPOption func(*httpTransport)

// WithClient substitutes the underlying *http.Client.
func WithClient(c *http.Client) HTTPOption
```

**R21** — `Do` returns an error only for failures to _obtain_ a response (dial, TLS, ctx cancellation). Non-2xx statuses are returned as a normal `Response` — classification (retry? error event?) is the caller's job. This keeps the interface honest for cassette replay of error responses.

**R22** — Protocol implementations must read `Response.Body` incrementally (never `io.ReadAll` on streaming endpoints) so cassette replay can preserve chunk boundaries and real streaming stays real.

Middleware composes naturally; the library ships none, but documents the pattern:

```go
type loggingTransport struct{ inner transport.Interface }

func (l loggingTransport) Do(ctx context.Context, req *transport.Request) (*transport.Response, error) {
    log.Printf("→ %s %s (%d bytes)", req.Method, req.URL, len(req.Body))
    return l.inner.Do(ctx, req)
}
```

---

## 7. The `cassette` Package

Record/replay at the transport layer. Cassettes contain real provider traffic and are committed to the repo; tests replay them with zero network and zero keys.

### 7.1 API

```go
package cassette

// New returns a replaying transport for testdata/cassettes/<name>.json.
// If the environment variable RECORD is non-empty, it instead records
// through transport.HTTP() and (re)writes the file on t.Cleanup.
// Replay failures (missing file, request mismatch, unconsumed
// interactions at Cleanup) fail the test with a diff.
func New(t testing.TB, name string) transport.Interface

// Replay always replays; fails the test if the file is missing.
func Replay(t testing.TB, name string) transport.Interface

// Record always records through inner, writing to path on Close.
// (New/Replay cover tests; Record exists for standalone capture tools.)
type Recorder interface {
    transport.Interface
    io.Closer
}

func Record(path string, inner transport.Interface) Recorder
```

### 7.2 File format

`testdata/cassettes/<name>.json`, pretty-printed, stable field order:

```json
{
  "version": 1,
  "interactions": [
    {
      "request": {
        "method": "POST",
        "url": "https://api.anthropic.com/v1/messages",
        "header": {
          "Anthropic-Version": ["2023-06-01"],
          "Content-Type": ["application/json"],
          "X-Api-Key": ["REDACTED"]
        },
        "body_json": { "model": "claude-sonnet-4-5", "stream": true, "…": "…" }
      },
      "response": {
        "status": 200,
        "header": { "Content-Type": ["text/event-stream"] },
        "chunks": [
          "event: message_start\ndata: {\"type\":\"message_start\",…}\n\n",
          "event: content_block_delta\ndata: {…}\n\n"
        ]
      }
    }
  ]
}
```

**R23 — Recording.** Request bodies with JSON content-type are stored as `body_json` (parsed, pretty) for readable diffs; anything else as `body_b64`. Response bodies are stored as a `chunks` array, one entry per `Read` that returned data, preserving streaming boundaries; chunks that are valid UTF-8 are stored as strings, otherwise the interaction stores `chunks_b64`. Sensitive headers are redacted on write → `["REDACTED"]`: the credentials `Authorization`, `X-Api-Key`, `X-Goog-Api-Key`, `Cookie`, `Set-Cookie`, plus the account and request identity providers echo back on responses — `Openai-Organization`, `Openai-Project`, `Anthropic-Organization-Id`, `Anthropic-Workspace-Id`, `Request-Id`, `X-Request-Id`, `Cf-Ray`. The second group is not secret, but cassettes are committed to a public repo and need not name the account that recorded them. Query parameter `key` (Gemini uses it) is rewritten to `key=REDACTED` in the stored URL.

**R24 — Replay.** Interactions are consumed strictly in order. Each incoming request must match the next stored interaction on: method, URL (with `key=…` masked before comparing), and body — compared as canonicalized JSON when both are JSON (so map ordering doesn't matter), byte-equal otherwise. Redacted headers are ignored during matching; all other headers are ignored too (matching on method+URL+body is sufficient and keeps cassettes robust against header churn). On mismatch: fail with a unified diff of expected vs actual body. Replayed responses deliver one stored chunk per `Read` call, so SSE parsing is exercised against real chunk boundaries.

**R25 — Re-record workflow.** `RECORD=1 go test ./agentkit/llm/... -run TestAnthropicToolCall` hits real APIs using ambient env keys and rewrites just that cassette. CI never sets `RECORD`. Redaction (R23) covers credentials and account identity automatically, but *prompt* content is stored verbatim: the README states that re-recorded cassettes must be read before commit, and `TestCassetteHygiene` backstops the mechanical part.

### 7.3 Usage in tests

```go
func TestAnthropicToolCall(t *testing.T) {
    client := llm.New(
        llm.WithTransport(cassette.New(t, "anthropic_tool_call")),
        llm.WithAPIKey("anthropic", apiKeyOrDummy()), // dummy under replay
    )
    msg, err := client.Complete(ctx, llm.Request{
        Model:    llm.ClaudeSonnet45,
        System:   "You are a test assistant.",
        Messages: []llm.Message{llm.UserText("What's the weather in Paris?")},
        Tools:    []llm.ToolDef{weatherToolDef},
    })
    if err != nil { t.Fatal(err) }
    if msg.StopReason != llm.StopToolUse { t.Fatalf("stop=%s", msg.StopReason) }
    // …assert on the tool_call block…
}

func apiKeyOrDummy() string {
    if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" { return k }
    return "test-key" // replay never checks it; recording requires a real one
}
```

---

## 8. Wire Protocols

Each protocol is one unexported method on `*Client`, in its own file inside package `llm`:

```go
// llm/anthropic.go, llm/openai_chat.go, llm/openai_responses.go, llm/gemini.go
func (c *Client) streamAnthropic(ctx context.Context, req Request, apiKey string, emit func(Event) bool)
```

`Client.Stream` normalizes the request (§5.3), resolves the key per R11 (`-` means send nothing), then dispatches on `Model.API` to one of the four. Each implementation: (1) builds the JSON payload from the normalized request, (2) issues it through the transport, (3) decodes the SSE/chunk stream into raw `llm.Event`s passed to `emit`, which `llm.NewStream` folds into the message.

They live in package `llm` rather than under `internal/` because they take a `*Client` and speak in `llm` types; a separate package would need an import cycle or a duplicate set of types. The shared `llm/internal/sse` package parses `text/event-stream` framing (event/data lines, multi-line data, `[DONE]` sentinel, CRLF tolerance) and is fuzz-tested.

The tables below define the mapping. Cassettes are ground truth for exact payloads; when the table and a provider's documented behavior disagree, follow the provider and update the table.

### 8.1 anthropic-messages

- **Endpoint:** `POST {BaseURL}/v1/messages` — headers `x-api-key`, `anthropic-version: 2023-06-01`, `content-type: application/json`. Body field `"stream": true`.
- **Request mapping:**

| Agentkit                        | Wire                                                                                                                                                |
| ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `System`                        | `system`: array of text blocks (cache_control on last per R15)                                                                                      |
| `MaxTokens`/`Model.MaxOutput`   | `max_tokens`                                                                                                                                        |
| `Reasoning`                     | `thinking: {"type":"enabled","budget_tokens":N}`; omit `temperature`                                                                                |
| `ToolDef`                       | `tools[]: {name, description, input_schema}`                                                                                                        |
| user text/image                 | `messages[]: {role:"user", content:[{type:"text"},{type:"image",source:{type:"base64",media_type,data}}]}`                                          |
| user document                   | `{type:"document", title?, source:{type:"base64",media_type,data}}`                                                                                 |
| assistant text                  | `{type:"text"}`                                                                                                                                     |
| assistant thinking (same model) | `{type:"thinking", thinking, signature}`; redacted → `{type:"redacted_thinking", data: Signature}`                                                  |
| assistant tool_call             | `{type:"tool_use", id, name, input}`                                                                                                                |
| tool_result message             | user-role message: `{type:"tool_result", tool_use_id, is_error, content:[text/image blocks]}`; consecutive tool results merge into one user message |

- **SSE decode:** `message_start` → `EventStart` + input/cache usage; `content_block_start` (types `text`/`thinking`/`redacted_thinking`/`tool_use`) → `*_start`; `content_block_delta` with `text_delta`/`thinking_delta`/`signature_delta`/`input_json_delta` → deltas (signature accumulates into `Block.Signature`); `content_block_stop` → `*_end`; `message_delta` → stop reason + output usage; `message_stop` → `EventDone`. `event: error` or non-2xx → `EventError`.
- **Stop reasons:** `end_turn`→`stop`, `max_tokens`→`length`, `tool_use`→`tool_use`, `refusal`→`error`.

### 8.2 openai-chat (Chat Completions)

- **Endpoint:** `POST {BaseURL}/chat/completions` — `Authorization: Bearer`. Body: `"stream": true`, plus `"stream_options":{"include_usage":true}` unless `Quirks.NoStreamUsage`.
- **Request mapping:**

| Agentkit        | Wire                                                                                                                                                                               |
| --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `System`        | first message `{role:"system", content}`                                                                                                                                           |
| `MaxTokens`     | `max_completion_tokens` (or `Quirks.MaxTokensField`)                                                                                                                               |
| `Reasoning`     | `reasoning_effort` unless `Quirks.NoReasoningEffort`                                                                                                                               |
| `ToolDef`       | `tools[]: {type:"function", function:{name, description, parameters}}`                                                                                                             |
| user text/image | `{role:"user", content:[{type:"text"},{type:"image_url", image_url:{url:"data:<mime>;base64,<data>"}}]}`                                                                           |
| user document   | `{type:"file", file:{filename?, file_data:"data:<mime>;base64,<data>"}}`                                                                                                           |
| assistant       | `{role:"assistant", content, tool_calls:[{id, type:"function", function:{name, arguments:<string>}}]}`; thinking blocks dropped (R17 already textified cross-model ones)           |
| tool_result     | `{role:"tool", tool_call_id, content:<text>}`; image blocks in tool results move to an immediately following user message (`content:[image]`), since role:tool cannot carry images |

- **SSE decode:** chunks `data: {choices:[{delta:{content?, tool_calls?:[{index,id?,function:{name?,arguments?}}]}, finish_reason?}], usage?}`; `data: [DONE]` ends. Text deltas open a text block lazily; `tool_calls[].index` maps to content indices (offset past any text block). `finish_reason`: `stop`→`stop`, `length`→`length`, `tool_calls`→`tool_use`, `content_filter`→`error`.

### 8.3 openai-responses (Responses API)

- **Endpoint:** `POST {BaseURL}/responses` — `Authorization: Bearer`. Body: `"stream": true`, `"store": false`, `"include": ["reasoning.encrypted_content"]`.
- **Request mapping:**

| Agentkit                        | Wire                                                                                                                                                                       |
| ------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `System`                        | `instructions`                                                                                                                                                             |
| `MaxTokens`                     | `max_output_tokens`                                                                                                                                                        |
| `Reasoning`                     | `reasoning: {"effort": "..."}`                                                                                                                                             |
| `ToolDef`                       | `tools[]: {type:"function", name, description, parameters, strict:false}`                                                                                                  |
| user msg                        | `input[]: {type:"message", role:"user", content:[{type:"input_text"},{type:"input_image", image_url:"data:…"}]}`                                                           |
| user document                   | `{type:"input_file", filename?, file_data:"data:<mime>;base64,<data>"}`                                                                                                    |
| assistant text                  | `{type:"message", role:"assistant", content:[{type:"output_text"}]}`                                                                                                       |
| assistant thinking (same model) | `{type:"reasoning", id?, encrypted_content}` reconstructed from `Block.Signature` (Signature stores JSON `{"id":"…","encrypted_content":"…"}`); cross-model → text per R17 |
| assistant tool_call             | `{type:"function_call", call_id, name, arguments:<string>}`                                                                                                                |
| tool_result                     | `{type:"function_call_output", call_id, output:<text>}`; images → following user message, as in §8.2                                                                       |

- **SSE decode:** `response.created` → `EventStart`; `response.output_item.added` opens blocks by item type (`message`→text, `reasoning`→thinking, `function_call`→tool call); `response.output_text.delta` → text delta; `response.reasoning_summary_text.delta` → thinking delta; `response.function_call_arguments.delta` → tool-call delta; `response.output_item.done` closes the block (capture `call_id`/`name`, reasoning id + encrypted_content into Signature); `response.completed` → usage + `EventDone`; `response.failed` / `error` events → `EventError`. Usage: `input_tokens`, `output_tokens`, `input_tokens_details.cached_tokens` → `CacheRead`.

### 8.4 google-gemini

- **Endpoint:** `POST {BaseURL}/v1beta/models/{Model.ID}:streamGenerateContent?alt=sse` — header `x-goog-api-key`.
- **Request mapping:**

| Agentkit        | Wire                                                                                                                                                                                        |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `System`        | `systemInstruction: {parts:[{text}]}`                                                                                                                                                       |
| `MaxTokens`     | `generationConfig.maxOutputTokens`                                                                                                                                                          |
| `Reasoning`     | `generationConfig.thinkingConfig: {thinkingBudget:N, includeThoughts:true}`                                                                                                                 |
| `ToolDef`       | `tools:[{functionDeclarations:[{name, description, parameters}]}]` — schema sanitized: strip `additionalProperties`, `$schema`, `format` values Gemini rejects; `enum` kept only on strings |
| user text/image | `contents[]: {role:"user", parts:[{text},{inlineData:{mimeType,data}}]}`                                                                                                                    |
| user document   | `{inlineData:{mimeType:"application/pdf",data}}` — the same part as an image; the mime type is the whole difference                                                                          |
| assistant       | `{role:"model", parts:[{text}, {text, thought:true, thoughtSignature}, {functionCall:{name,args}}]}`                                                                                        |
| tool_result     | `{role:"user", parts:[{functionResponse:{name, response:{output:<text>}}}]}`; images as additional `inlineData` parts                                                                       |

- **SSE decode:** each `data:` line is a full `GenerateContentResponse`; `candidates[0].content.parts[]` append: `text` parts → text deltas (parts with `thought:true` → thinking deltas, `thoughtSignature` → Signature); `functionCall` parts arrive whole → emit `tool_call_start` + one `tool_call_delta` (full args) + `tool_call_end` (document: Gemini does not stream partial args); `usageMetadata` → usage (`promptTokenCount`, `candidatesTokenCount`, `cachedContentTokenCount`→CacheRead, `thoughtsTokenCount` counted inside output); `finishReason`: `STOP`→`stop` (or `tool_use` if any functionCall part was emitted), `MAX_TOKENS`→`length`, `SAFETY`/`RECITATION`→`error`.
- Empty function-call IDs → synthesize per R18.

---

## 9. The `llmtest` Package: Scripted Fake

A deterministic, zero-network `llm.Streamer` for agent-loop tests.

```go
package llmtest

// Fake implements llm.Streamer with a scripted queue of replies.
type Fake struct { /* unexported */ }

func New(replies ...Reply) *Fake

type Reply struct {
    Blocks     []llm.Block
    StopReason llm.StopReason // "" → inferred: tool_use if any tool_call block, else stop
    Err        error          // if set, the stream ends with EventError instead
}

// Convenience constructors:
func Text(text string) Reply
func ToolCall(name string, args any) Reply            // args is json.Marshal-ed
func Blocks(stop llm.StopReason, blocks ...llm.Block) Reply
func Error(err error) Reply

// Append adds replies to the live queue (safe mid-run, from any goroutine).
func (f *Fake) Append(replies ...Reply)

// Requests returns a copy of every llm.Request received so far —
// assert on system prompt, message history, model ID, tools.
func (f *Fake) Requests() []llm.Request

// Stream implements llm.Streamer. It pops the next Reply and synthesizes
// a valid event sequence per R6: text/thinking split into word-level
// deltas, tool-call args streamed as two JSON fragments, usage estimated
// at len(text)/4 tokens. An empty queue yields EventError
// ("llmtest: no replies queued") — a loud test failure, not a hang.
// It honors ctx cancellation between events (StopAborted), which lets
// tests exercise interrupt paths deterministically.
func (f *Fake) Stream(ctx context.Context, req llm.Request) *llm.Stream
```

**R26** — `Fake.Stream` builds streams exclusively through `llm.NewStream` (R10), so fake streams exercise the same accumulator code paths as real ones.

**R27** — `Reply.Blocker` (a `<-chan struct{}`) makes the stream wait on the channel before emitting its terminal event; tests use it to deterministically overlap `Send`/`Interrupt`/`SetModel` with an in-flight call (no sleeps in tests, ever).

---

## 10. The `agentkit` Package: Tools

### 10.1 Tool type

```go
package agentkit

type ToolCall struct {
    ID   string
    Name string
    Args json.RawMessage
}

type ToolResult struct {
    Blocks []llm.Block // text and/or images sent back to the model
    // Details is app-facing data (UIs, logs); never sent to the model.
    // Must be JSON-serializable if session state will be persisted.
    Details any
}

func Text(format string, a ...any) ToolResult // fmt.Sprintf → single text block

type Tool struct {
    Name        string
    Description string
    Schema      json.RawMessage // JSON Schema for arguments
    // Sequential forces the whole batch containing this tool to run
    // one-at-a-time (see R33).
    Sequential bool
    // Execute runs the tool. Returning an error produces an is_error
    // tool result carrying err.Error() — the model sees it and can retry.
    Execute func(ctx context.Context, call ToolCall) (ToolResult, error)
}
```

### 10.2 Typed constructor

```go
// NewTool derives Schema from Args by reflection and decodes+validates
// arguments before fn runs. Args must be a struct.
func NewTool[Args any](name, description string,
    fn func(ctx context.Context, args Args) (ToolResult, error)) Tool
```

```go
type WriteFileArgs struct {
    Path    string `json:"path" jsonschema:"description=Absolute file path"`
    Content string `json:"content" jsonschema:"description=Full file content"`
    Mode    string `json:"mode,omitempty" jsonschema:"description=create or overwrite,enum=create|overwrite,default=overwrite"`
}

var writeFile = agentkit.NewTool("write_file", "Write a file to disk",
    func(ctx context.Context, a WriteFileArgs) (agentkit.ToolResult, error) {
        if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
            return agentkit.ToolResult{}, err
        }
        return agentkit.Text("wrote %d bytes to %s", len(a.Content), a.Path), nil
    })
```

**R28 — Schema generation** (`schema.go`, reflection, no dependencies). Struct → `{"type":"object","properties":{…},"required":[…],"additionalProperties":false}`. Field name from `json` tag (skip `json:"-"`). Required unless the field is a pointer or has `,omitempty`. Type mapping: `string`→string, `bool`→boolean, all int kinds→integer, floats→number, slice→array(+items), `map[string]T`→object(+additionalProperties schema), nested struct→object (recursive), `*T`→schema of T (optional). `jsonschema` tag directives, comma-separated: `description=…`, `enum=a|b|c` (strings only), `default=…`, `minimum=…`, `maximum=…`. Unknown directives are an error at `NewTool` time (fail fast, at startup). Recursion depth > 16 or unsupported kinds (chan, func, interface) panic at construction with a clear message — tool definition bugs are programmer errors.

**R29 — Argument validation.** Before `Execute`, args are validated against the schema by an internal minimal validator (type checks, required, enum, min/max, additionalProperties) and decoded with `json.Unmarshal` into `Args` (which enforces types a second time). Validation failure produces an `is_error` tool result listing each failing path (`args.path: expected string, got number`) — the model can correct and retry. `Execute` is never called with invalid args.

### 10.3 Progress updates

```go
// Progress emits a tool_update event from inside a running tool.
// No-op if ctx does not originate from a session run.
func Progress(ctx context.Context, update ToolResult)
```

The session injects an emitter into the context passed to `Execute`.

---

## 11. The `agentkit` Package: Session

The single stateful primitive. A session owns conversation history, the current model, tools, and the two message queues.

### 11.1 API

```go
type Config struct {
    LLM    llm.Streamer // required: *llm.Client or llmtest.Fake
    Model  llm.Model    // required
    System string
    Tools  []Tool

    Reasoning llm.Effort

    // BeforeTool runs after validation, before Execute. Returning a non-nil
    // error blocks the call: Execute is skipped and the error becomes an
    // is_error tool result the model sees. Policy enforcement hook.
    BeforeTool func(ctx context.Context, call ToolCall) error

    // MaxTurns caps LLM calls per Run (runaway protection). 0 → 40.
    MaxTurns int
}

func New(cfg Config) *Session

// State is the JSON-serializable snapshot of a session.
type State struct {
    System    string        `json:"system"`
    Model     llm.Model     `json:"model"`
    Reasoning llm.Effort    `json:"reasoning"`
    Messages  []llm.Message `json:"messages"`
}

// Resume rebuilds a session from a snapshot. cfg.Model/System/Reasoning
// are overridden by the snapshot; cfg supplies LLM, Tools, hooks.
func Resume(cfg Config, st State) *Session

func (s *Session) State() State // deep-copied snapshot; safe mid-run

// Run appends a user message and executes the loop, yielding events as
// they happen. The loop runs inside the iterator (pull model): breaking
// early interrupts the run. Only one Run/Continue may be active; a
// concurrent call yields a single run_end event with Err = ErrBusy.
func (s *Session) Run(ctx context.Context, prompt string) iter.Seq[Event]
func (s *Session) RunMessage(ctx context.Context, msg llm.Message) iter.Seq[Event]

// Continue re-enters the loop without a new prompt: after an error or
// abort, or to drain queued messages. Yields run_end with
// Err = ErrNothingToDo if there is nothing to continue from.
func (s *Session) Continue(ctx context.Context) iter.Seq[Event]

// —— Callable from any goroutine, any time: ——

// Send queues a steering message, injected at the next turn boundary of
// the active run. If idle, it is consumed by the next Run/Continue.
func (s *Session) Send(prompt string)
func (s *Session) SendMessage(msg llm.Message)

// FollowUp queues a message delivered only when the run would otherwise
// finish; the run then continues with it instead of ending.
func (s *Session) FollowUp(prompt string)

// Interrupt aborts the in-flight LLM call or tool batch. The partial
// assistant message is kept with StopReason StopAborted; the run ends.
// No-op when idle.
func (s *Session) Interrupt()

// SetModel switches models; takes effect at the next LLM call, mid-run
// included. Cross-model history handoff is automatic (§5.3).
func (s *Session) SetModel(m llm.Model)
func (s *Session) SetReasoning(e llm.Effort)

func (s *Session) ClearQueues()

var (
    ErrBusy         = errors.New("agentkit: a run is already active")
    ErrNothingToDo  = errors.New("agentkit: nothing to continue")
    ErrMaxTurns     = errors.New("agentkit: max turns exceeded")
)
```

Convenience for callers who don't need streaming:

```go
// Final drains an event sequence and returns the last assistant message.
func Final(events iter.Seq[Event]) (llm.Message, error)

// usage:
msg, err := agentkit.Final(session.Run(ctx, "add a --verbose flag"))
```

### 11.2 Session events

```go
type EventType string

const (
    EventRunStart   EventType = "run_start"
    EventTurnStart  EventType = "turn_start"   // one LLM call + its tool batch
    EventModel      EventType = "model"        // wraps one llm.Event (deltas etc.)
    EventMessage    EventType = "message"      // a message was appended to history
    EventToolStart  EventType = "tool_start"
    EventToolUpdate EventType = "tool_update"  // via agentkit.Progress
    EventToolEnd    EventType = "tool_end"
    EventTurnEnd    EventType = "turn_end"
    EventRunEnd     EventType = "run_end"      // always the last event
)

type Event struct {
    Type EventType
    Turn int // 1-based turn counter within the run

    Stream *llm.Event    // EventModel
    Message *llm.Message // EventMessage: the appended message (user, assistant, or tool_result)
    Call   *ToolCall     // EventToolStart/Update/End
    Result *ToolResult   // EventToolUpdate (partial) / EventToolEnd (final)
    ToolErr error        // EventToolEnd when the tool failed or was blocked

    // Err on EventRunEnd: nil = clean finish; otherwise ErrBusy, ErrMaxTurns,
    // ctx.Err(), the LLM error, or ErrInterrupted-wrapped context.Canceled.
    Err error
}
```

**R30 — Event echo.** Every mutation of history is announced: `EventMessage` fires for the initial user message, each final assistant message, each tool result, and each injected steering/follow-up message — in exactly the order they are appended. Replaying `EventMessage` payloads reconstructs `State().Messages`.

### 11.3 The loop, precisely

```
Run(ctx, prompt):
  acquire run slot (CAS); on failure → yield run_end{Err: ErrBusy}; return
  emit run_start
  append user message; emit message
  turn := 0
  loop:
    turn++
    if turn > MaxTurns → end(ErrMaxTurns)
    emit turn_start
    req := build llm.Request from (system, model, reasoning, history, tool defs)
    stream := cfg.LLM.Stream(runCtx, req)
    for ev := range stream.Events(): emit model{ev}      // consumer sees deltas live
    msg, llmErr := stream.Message()
    append msg (if it has content or an error); emit message
    if msg.StopReason == StopError   → end(llmErr)
    if msg.StopReason == StopAborted → end(ErrInterrupted or ctx.Err())
    calls := tool_call blocks in msg
    if len(calls) > 0:
        execute batch (R33): for each call →
            emit tool_start
            validate (R29); BeforeTool; Execute with Progress-carrying ctx
            emit tool_end (ToolErr set on failure/block)
        append one tool_result message per call, in assistant block order,
        emitting message for each
        if interrupted mid-batch → synthesize "interrupted" error results
            for unfinished calls (R16 pairing), append+emit, end(ErrInterrupted)
    emit turn_end
    steering := drain steering queue
    if len(steering) > 0: append+emit each; continue loop
    if len(calls) > 0: continue loop            // model must see tool results
    followUp := pop one follow-up
    if followUp != nil: append+emit; continue loop
    end(nil)

end(err): emit run_end{Err: err}; release run slot
```

**R31 — Steering.** The steering queue is drained _completely_ at each turn boundary (after tool results, before the next LLM call). Steering never aborts the in-flight call — that is `Interrupt`'s job. `Send` while idle queues; the next `Run`/`Continue` drains at its first boundary check.

**R32 — Interrupt semantics.** `Interrupt` cancels an internal per-run context. During an LLM call: the stream ends `StopAborted`, the partial assistant message is kept. During tools: running `Execute`s get ctx cancellation and their results (or errors) are recorded; not-yet-started calls get synthetic `is_error` results (`"interrupted before execution"`). History is always left valid (every tool call answered, R16 never needed for our own output). `Continue(ctx)` afterwards resumes cleanly.

**R33 — Tool batch execution.** If any called tool has `Sequential: true`, the whole batch runs sequentially in assistant block order. Otherwise all calls run concurrently (one goroutine each). Regardless of completion order, `tool_result` messages are appended and emitted in assistant block order. `tool_start`/`tool_end` events fire in real execution order. Unknown tool name → `is_error` result `"unknown tool: <name>"`, no execution.

**R34 — Model switching.** `SetModel`/`SetReasoning` store atomically and are read once per `build llm.Request`. Switching providers mid-run "just works" because handoff normalization (R16–R18) runs on every request build.

**R35 — Concurrency contract.** `Send`, `SendMessage`, `FollowUp`, `Interrupt`, `SetModel`, `SetReasoning`, `ClearQueues`, `State` are safe from any goroutine at any time. `Run`/`RunMessage`/`Continue` are mutually exclusive (ErrBusy). The event iterator must be consumed from a single goroutine. All of this is verified under `go test -race`.

**R36 — Persistence.** `State()` deep-copies. `json.Marshal(session.State())` at any `EventMessage` or `EventRunEnd` boundary is a consistent snapshot; `Resume` + `Continue` recovers to the last message boundary (mid-stream progress is lost by design — streams are not resumable).

### 11.4 End-to-end example

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/oliverkofoed/gokit/agentkit"
    "github.com/oliverkofoed/gokit/agentkit/llm"
)

func main() {
    client := llm.New() // transport.HTTP(), keys from env

    session := agentkit.New(agentkit.Config{
        LLM:    client,
        Model:  llm.ClaudeSonnet45,
        System: "You are a careful coding agent. Prefer small, verifiable steps.",
        Tools:  []agentkit.Tool{readFile, writeFile, runCommand},
        Reasoning: llm.EffortMedium,
        BeforeTool: func(ctx context.Context, call agentkit.ToolCall) error {
            if call.Name == "run_command" && !allowed(call) {
                return fmt.Errorf("blocked by policy")
            }
            return nil
        },
    })

    // From another goroutine (UI, RPC handler…):
    //   session.Send("actually, target Go 1.22 compatibility")
    //   session.SetModel(llm.GPT5Mini)
    //   session.Interrupt()

    for ev := range session.Run(context.Background(), "add a --verbose flag to cmd/serve") {
        switch ev.Type {
        case agentkit.EventModel:
            if ev.Stream.Type == llm.EventTextDelta {
                fmt.Print(ev.Stream.Delta)
            }
        case agentkit.EventToolStart:
            fmt.Printf("\n[%s %s]\n", ev.Call.Name, ev.Call.Args)
        case agentkit.EventRunEnd:
            if ev.Err != nil {
                fmt.Fprintln(os.Stderr, "run failed:", ev.Err)
            }
        }
    }

    // Persist for later resumption anywhere:
    snapshot, _ := json.Marshal(session.State())
    _ = os.WriteFile("session.json", snapshot, 0o600)
}
```

### 11.5 Context compaction (a pattern, not a feature)

The library ships no compaction. The building blocks make it an application
pattern, including mid-run — this belongs in `examples/` and the README, and
exists here so implementers don't "helpfully" add a compaction subsystem:

1. **Measure with ground truth, not estimates.** Every assistant message
   carries `Usage.Input` — the provider's own count of the prompt it just
   processed. Compare it to `Model.ContextWindow` at each `EventMessage`
   (assistant) or `EventTurnEnd`.
2. **Stop cleanly.** Break out of the `Run` iterator (= interrupt, R32) or
   compact between runs. History is always left valid.
3. **Summarize with a plain LLM call**, rebuild state, resume:

```go
func compact(ctx context.Context, client *llm.Client, s *agentkit.Session) (*agentkit.Session, error) {
    st := s.State()
    cut := len(st.Messages) - 6 // keep a recent tail; never split a
    cut = adjustToTurnBoundary(st.Messages, cut) // tool_call/tool_result pair

    summary, err := client.Complete(ctx, llm.Request{
        Model:  st.Model,
        System: "Summarize this conversation for an agent that will continue it. " +
            "Preserve: the task, decisions made, files read/modified, open problems.",
        Messages: append(slices.Clone(st.Messages[:cut]),
            llm.UserText("Summarize the conversation above.")),
    })
    if err != nil { return nil, err }

    st.Messages = append([]llm.Message{
        agentkit.NoteCompaction(cut),                    // Kind message (R37): app-visible marker
        llm.UserText("Summary of the conversation so far:\n" + textOf(summary)),
    }, st.Messages[cut:]...)
    return agentkit.Resume(cfgFor(client), st), nil
}
```

(`NoteCompaction` is app code: `llm.AppMessage("compaction", …)`.)

If two real consumers end up duplicating this, the sanctioned library
extension is a `TransformRequest func(ctx, []llm.Message) []llm.Message`
field on `Config` (what pi calls `transformContext`: per-call view rewriting
without touching stored history) — not an in-library compaction engine.
pi's full subsystem (cut-point selection, split-turn handling, incremental
summaries, file-operation tracking) stays out of scope; see §14.

---

## 12. Test Suite

Everything runs offline by default (`go test ./...`), with `-race` in CI. Two mechanisms per P3/§9: **cassettes** prove wire correctness against real recorded traffic; the **fake** proves loop logic deterministically. No test may sleep for synchronization (use `llmtest.Blocker`, channels).

### 12.1 `llm` core (`llm/*_test.go`)

| Test                             | Rule    | What it verifies                                                                                                                                               |
| -------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TestMessageJSONRoundTrip`       | R1,R3   | fuzz+table: Model/Message/Block/State survive marshal→unmarshal byte-identically                                                                               |
| `TestComputeCost`                | R4      | cost math incl. cache read/write                                                                                                                               |
| `TestStreamAccumulator*` (10 tests) | R6,R10 | `NewStream` builds correct partials for interleaved blocks; exactly one terminal event; panic in producer → EventError                                         |
| `TestPartialJSON`                | R7      | partial arg parsing: truncated strings, open arrays, nested objects, unicode split across chunks                                                               |
| `TestStreamAccumulatorMessageFirst`, `TestStreamAccumulatorBreakEarly` | R9 | second `Events()` range is empty; `Message()` idempotent                                                                                                       |
| `TestKeyResolution`              | R11     | precedence table incl. KeyFunc, env fallback, ErrNoAPIKey, `-` sentinel                                                                                        |
| `TestClient*` (retry family, 8 tests) | R12 | 429→retry with Retry-After; 500→backoff; no retry after first content event; ctx cancel during backoff                                                         |
| `TestConvertOrphanInjection` | R16     | synthetic results injected                                                                                                                                     |
| `TestConvertCrossModelThinking`         | R17     | thinking→`<thinking>` text across models; preserved same-model                                                                                                 |
| `TestConvertIDSanitization`    | R18     | invalid + empty IDs, call/result consistency                                                                                                                   |
| `TestConvertDocumentDowngrade`          | R38 | document downgrade, and that the caller's blocks are not mutated                                                                                    |
| `TestAnthropicDocumentInput`, `TestOpenAIChatDocumentInput`, `TestOpenAIResponsesDocumentInput`, `TestGeminiDocumentInput` | §8 | document encoding per protocol                            |
| `Test{Anthropic,OpenAIChat,OpenAIResponses}DocumentWithoutName` | §8 | an unnamed document omits the title/filename field rather than sending it empty |
| `Test{Anthropic,OpenAIChat,OpenAIResponses,Gemini}DocumentInToolResultDropped` | R38 | a document in a tool result is dropped, and its bytes never reach the wire |
| `TestConvertEmptyAndVision`             | R19,R20 | empty-message and non-vision handling                                                                                                                          |
| `TestConvertKindRemoval`, `TestMessageJSONRoundTrip` | R37 | Kind messages round-trip JSON; normalization removes them before R16–R20 (an orphaned tool call followed only by a Kind message still gets a synthetic result) |

### 12.2 Wire protocols (`llm/{anthropic,openai_chat,openai_responses,gemini}.go`, cassette-driven)

One table-driven suite (`llm/cassette_suite_test.go`) runs the same scenario matrix for **each** of the four protocols — one cassette per cell, named `<protocol>_<scenario>`. A protocol may declare a scenario inapplicable via `casProvider.Skip`, which reports the reason rather than silently passing:

| Scenario                                      | Asserts                                                                                  |
| --------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `basic_text`                                  | request payload golden (captured transport.Request), text deltas, StopEnd, usage present |
| `tool_call`                                   | tool def encoding, streamed args, StopToolUse, complete Args JSON                        |
| `tool_result_roundtrip`                       | second request replays call+result correctly (pairing, IDs)                              |
| `thinking`                                    | reasoning param mapping (R14), thinking deltas, signature captured                       |
| `thinking_replay`                             | same-model replay sends signatures/encrypted content back                                |
| `image_input`                                 | base64 encoding per protocol                                                             |
| `image_in_tool_result`                        | §8.2/8.3 user-message spillover; Gemini inlineData                                       |
| `document_input`                              | document encoding per protocol; the provider extracts the PDF's text                     |
| `multi_tool_parallel`                         | multiple tool_call blocks in one message, index mapping                                  |
| `error_400`                                   | HTTP error body lands in ErrorText, StopError                                            |
| `abort_mid_stream`                            | ctx cancel mid-SSE → StopAborted + partial content                                       |
| `unicode`                                     | emoji/surrogates split across SSE chunk boundaries                                       |
| `caching` (anthropic + openrouter quirk only) | breakpoint placement (R15), cache usage fields                                           |
| `handoff_from_<other>`                        | history recorded by another provider replays (R16–R18)                                   |

Declared skips: `openai-chat` has no `thinking`/`thinking_replay` cell — Chat Completions accepts `reasoning_effort` but returns no reasoning content, so there is nothing to assert or replay; its request-side mapping is covered by the scripted quirk tests instead. `caching` runs only for a provider that sets `casProvider.CacheModel`, i.e. one with explicit prompt-cache breakpoints (R15) — today, anthropic.

Plus per-protocol quirk tests (openai-chat only, cassette or golden-payload): `max_tokens_field`, `no_stream_usage`, `no_reasoning_effort`.

`abort_mid_stream` works under replay: the test cancels ctx after N events; the cassette transport keeps serving chunks but the reader stops — deterministic.

### 12.3 Transport + cassette (`transport`, `cassette`)

- `TestHTTPStreaming`: against `httptest.Server` sending chunked SSE; verifies incremental reads (R22) and ctx cancellation mid-body.
- `TestCassetteRoundTrip`: record against `httptest.Server` → replay → byte-identical chunks and chunk boundaries.
- `TestCassetteRedaction` (R23): auth headers and `?key=` never reach disk (scan file bytes for the secret).
- `TestCassetteMismatch` (R24): wrong body/URL/method → test failure with diff. `TestCassetteLeftoverInteractions`: unconsumed interactions → failure.
- `TestCassetteJSONKeyOrder`: same JSON, different key order → matches.
- `TestCassetteHygiene` (R23, in `llm`): scans every committed cassette for credential shapes, unredacted identity headers, unmasked `key=`, and dead-account error markers. This is the gate that keeps recorded traffic publishable.

### 12.4 SSE parser (`llm/internal/sse`)

- `TestSSE` table tests: multi-line data, comment lines, CRLF, missing final newline, `[DONE]`. `TestSSEHugeEvent`, `TestSSESizeCap`, `TestSSESizeCapAcrossLines`, `TestSSEUnderlyingError` cover the size cap and reader errors.
- `FuzzSSE`: never panics, never allocates unbounded memory on adversarial input.

### 12.5 Tools + schema (`agentkit`)

- `TestSchemaGeneration` (R28): golden JSON schema for a kitchen-sink struct (nested, pointers, slices, maps, enums, omitempty).
- `TestSchemaUnsupported`: chan/func fields panic at NewTool with clear message.
- `TestValidation` (R29): each validator rule produces the documented error path string; valid args decode; `Execute` not called on invalid.
- `TestToolErrorBecomesResult`: returned error → is_error result with message.

### 12.6 Session loop (`agentkit`, fake-driven)

| Test                             | Rule  | Scenario                                                                                                           |
| -------------------------------- | ----- | ------------------------------------------------------------------------------------------------------------------ |
| `TestSingleTurn`                 | R30   | text reply → events run_start, message(user), model…, message(assistant), turn_end, run_end(nil)                   |
| `TestToolLoop`, `TestToolErrorBecomesResult` | — | tool_call reply → tool executes → results appended → second LLM call sees them (assert via `fake.Requests()`)      |
| `TestParallelBatchOrdering`      | R33   | 3 concurrent tools finishing out of order → results in assistant order; events in completion order                 |
| `TestSequentialToolForcesBatch`  | R33   | one Sequential tool → whole batch serialized                                                                       |
| `TestUnknownTool`                | R33   | error result, loop continues                                                                                       |
| `TestBeforeToolBlocks`           | —     | blocked call → is_error result with policy message, Execute not called                                             |
| `TestSteering`                   | R31   | Send during blocked tool → injected after turn, before next LLM call; multiple sends drain together                |
| `TestFollowUp`                   | R31   | delivered only when run would end; one per boundary                                                                |
| `TestSendWhileIdle`              | R31   | queued → consumed by next Run                                                                                      |
| `TestInterruptDuringLLM`         | R32   | Blocker reply + Interrupt → partial kept, StopAborted, run_end(ErrInterrupted); Continue works after               |
| `TestInterruptDuringTools`       | R32   | unfinished calls get synthetic results; history valid                                                              |
| `TestBreakingIteratorInterrupts` | P4    | consumer `break` → same as interrupt                                                                               |
| `TestModelSwitchMidRun`          | R34   | SetModel between turns → `fake.Requests()[1].Model` changed                                                        |
| `TestMaxTurns`                   | —     | tool-call loop forever → run_end(ErrMaxTurns) at cap                                                               |
| `TestBusy`                       | R35   | second Run during first → single run_end(ErrBusy), first unaffected                                                |
| `TestStateSnapshotAndResume` | R36, R37 | snapshot mid-conversation → JSON → Resume → Continue produces coherent history; a Kind message in state survives the round trip but never appears in `fake.Requests()` |
| `TestEventMessageReconstruction` | R30   | replaying EventMessage payloads == State().Messages                                                                |
| `TestLLMErrorEndsRun`            | P5    | fake Error reply → assistant msg persisted with StopError, run_end(err)                                            |
| `TestProgressEvents`, `TestProgressNoopOutsideSession` | §10.3 | Progress inside tool → tool_update events                                                                          |
| `TestRace`                       | R35   | hammer Send/Interrupt/SetModel/State from goroutines during a long fake run; `-race` clean                         |

### 12.7 Recording against real providers

There is no separate live-test harness or build tag. Recording is the same test code, re-run with `RECORD=1` and real keys in the environment (R25): the cassette transport then proxies to `transport.HTTP()` and rewrites the file it would otherwise replay. Everything else — CI included — runs from the committed cassettes with no keys and no network.

The examples are build-only checks (`go build ./agentkit/examples/...`); they are not exercised against cassettes.

---

## 13. Implementation Milestones

Each milestone compiles, passes its tests, and is independently reviewable.

1. **Types + stream core.** `llm` types, JSON round-trips, `NewStream` accumulator, partial-JSON parser. (§3, §4.1–4.2; tests 12.1 rows 1–5.)
2. **Transport + cassette + SSE.** (§6, §7, `internal/sse`; tests 12.3, 12.4.)
3. **Anthropic protocol + client.** Client with auth/retry, anthropic-messages end to end, first cassettes recorded. (§4.3, §5, §8.1; tests 12.1 rows 6–11, anthropic column of 12.2.)
4. **Remaining protocols.** openai-chat, openai-responses, gemini; full 12.2 matrix; handoff tests.
5. **`llmtest` fake.** (§9; used by everything in milestone 6.)
6. **Tools + schema + session.** (§10, §11; tests 12.5, 12.6.)
7. **Examples, README, cassette hygiene.** (`go vet`, `-race`, `TestCassetteHygiene`.)

---

## 14. Non-Goals

Explicitly out of scope for v1 (revisit only with a concrete consumer need):

- OAuth flows and credential stores (the `WithKeyFunc` seam is where they plug in externally).
- Generated model catalogs / models.dev sync.
- Image generation, audio, embeddings.
- MCP, sub-agents, context compaction, prompt templates, skills/hooks systems.
- Session storage backends (JSON snapshot is the primitive; storage is the caller's).
- A TUI, CLI, or orchestrator.
- Sandboxing or permissions beyond the `BeforeTool` hook — agentkit executes what you give it, with your process's privileges.
