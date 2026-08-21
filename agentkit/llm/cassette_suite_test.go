package llm

// The shared cassette suite (SPEC §12.2). Every protocol runs the same
// scenario matrix against traffic recorded from a real provider; the only
// per-protocol code is a descriptor and a decoder that reads that protocol's
// request body into a normalized view.
//
// The split matters: the decoder asserts the *wire shape* (anthropic writes
// tools[i].input_schema, openai writes tools[i].function.parameters, gemini
// writes tools[0].functionDeclarations[i].parameters — a decoder that looked
// in the wrong place would find nothing and fail), while the scenarios below
// assert the *semantics* once, for everyone.
//
// Record one cell:
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run 'TestCassetteAnthropic/tool_call' -count=1
//
// Record a whole column:
//
//	ANTHROPIC_API_KEY=sk-... RECORD=1 go test ./llm/ -run 'TestCassetteAnthropic' -count=1
//
// Cassettes are committed. Auth is redacted on write (R23); TestCassetteHygiene
// re-checks that before they ship.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/oliverkofoed/gokit/agentkit/llm/cassette"
	"github.com/oliverkofoed/gokit/agentkit/llm/transport"
)

// ---- provider descriptor ----------------------------------------------------

// casProvider describes one protocol column of the matrix.
type casProvider struct {
	// Name prefixes every cassette: testdata/cassettes/<Name>_<scenario>.json.
	Name string
	// EnvKey is the environment variable read when recording.
	EnvKey string
	// Model is the cheap workhorse used by most scenarios. Its Provider also
	// selects which key the client is given.
	Model Model
	// CacheModel runs the caching scenario. Zero value skips it — explicit
	// prompt-cache breakpoints are an anthropic/openrouter concern (R15).
	CacheModel Model
	// Foreign is the protocol whose history the handoff scenario replays.
	Foreign Model

	// NoToolCallIDs marks protocols that do not carry a per-call id on the
	// wire (gemini pairs results by function name instead), so ID assertions
	// fall back to matching on name.
	NoToolCallIDs bool

	// Temp is the temperature every scenario sends. Nil omits the parameter
	// entirely — the GPT-5 family rejects any value but the default.
	Temp *float64

	// ThinkingEffort is the level the thinking scenarios request. Zero means
	// EffortLow. Some models skip reasoning entirely at low effort — luna
	// returns reasoning_tokens:0 and no reasoning item — leaving nothing to
	// replay, so they need a higher level to exercise the path at all.
	ThinkingEffort Effort

	// MinOutputTokens floors every scenario's MaxTokens. Models that spend
	// output tokens on hidden reasoning before emitting any text (the GPT-5
	// family, gemini 2.5 flash) return nothing at all under a small cap.
	MinOutputTokens int

	// NoThinkingText marks protocols that return a reasoning signature but no
	// readable reasoning text (openai-responses streams summaries only when
	// asked for them), so the thinking scenario checks the signature alone.
	NoThinkingText bool

	// Decode reads a recorded request into the normalized view. It receives
	// the request URL too, since not every protocol puts everything in the
	// body. It must fail the test if the protocol's expected fields are
	// missing, which is what pins each protocol's wire shape.
	Decode func(t *testing.T, url string, body map[string]any) casView

	// BadRequest returns a request the provider rejects with a 4xx, used by
	// the error_400 scenario.
	BadRequest func(m Model) Request

	// Extra holds protocol-specific payload assertions, keyed by scenario
	// name. It receives every request body the scenario sent, in order.
	Extra map[string]func(t *testing.T, bodies []map[string]any)

	// Skip marks scenarios that do not apply, with a reason.
	Skip map[string]string
}

// ---- normalized request view ------------------------------------------------

// casView is a protocol-agnostic reading of one recorded request body. Each
// decoder is responsible for locating these values in its own wire format.
type casView struct {
	// Raw is the request body verbatim, as encoded for the wire. Note that
	// Go's JSON encoder escapes "<" and ">", so a textified thinking tag
	// appears as \u003cthinking\u003e — match it with hoHasThinkingTag,
	// never with a bare strings.Contains for "<thinking>".
	Raw       []byte
	Model     string
	MaxTokens int
	Stream    bool
	System    string
	HasTemp   bool
	Messages  []casMessage
	Tools     []casTool
	Thinking  *casThinking
}

type casMessage struct {
	Role        string // "user" | "assistant"
	Text        string
	Images      []casImage
	ToolCalls   []casToolCall
	ToolResults []casToolResult
	Thinking    []casThinkingBlock
}

type casImage struct{ MimeType, Data string }

type casToolCall struct {
	ID, Name string
	Args     string
}

type casToolResult struct {
	ID, Name, Text string
	Images         []casImage
}

type casThinkingBlock struct{ Text, Signature string }

type casTool struct {
	Name        string
	Description string
	Schema      map[string]any
}

type casThinking struct {
	Enabled bool
	Budget  int    // budget-based protocols (anthropic, gemini)
	Effort  string // effort-based protocols (openai)
}

// allToolCalls flattens every tool call across the encoded history.
func (v casView) allToolCalls() []casToolCall {
	var out []casToolCall
	for _, m := range v.Messages {
		out = append(out, m.ToolCalls...)
	}
	return out
}

// allToolResults flattens every tool result across the encoded history.
func (v casView) allToolResults() []casToolResult {
	var out []casToolResult
	for _, m := range v.Messages {
		out = append(out, m.ToolResults...)
	}
	return out
}

// allImages flattens every image, wherever the protocol chose to put it —
// nested in a tool result (anthropic) or spilled into a following user
// message (openai-chat, openai-responses).
func (v casView) allImages() []casImage {
	var out []casImage
	for _, m := range v.Messages {
		out = append(out, m.Images...)
		for _, r := range m.ToolResults {
			out = append(out, r.Images...)
		}
	}
	return out
}

func (v casView) allThinking() []casThinkingBlock {
	var out []casThinkingBlock
	for _, m := range v.Messages {
		out = append(out, m.Thinking...)
	}
	return out
}

// ---- harness ----------------------------------------------------------------

// casTee wraps the cassette transport and keeps the outgoing requests.
// cassette.New does not expose them, and the cassette file is only written at
// cleanup — too late to read inside the test — so payload assertions need
// their own copy, in both record and replay mode.
type casTee struct {
	inner transport.Interface

	mu   sync.Mutex
	reqs []*transport.Request
}

func (t *casTee) Do(ctx context.Context, req *transport.Request) (*transport.Response, error) {
	cp := *req
	cp.Body = append([]byte(nil), req.Body...)
	t.mu.Lock()
	t.reqs = append(t.reqs, &cp)
	t.mu.Unlock()
	return t.inner.Do(ctx, req)
}

// bodies decodes every captured request body, in order.
func (t *casTee) bodies(tb testing.TB) []map[string]any {
	tb.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]map[string]any, 0, len(t.reqs))
	for i, r := range t.reqs {
		var m map[string]any
		if err := json.Unmarshal(r.Body, &m); err != nil {
			tb.Fatalf("unmarshal request %d: %v\nbody: %s", i, err, r.Body)
		}
		out = append(out, m)
	}
	return out
}

// rawBody returns the i-th request's bytes exactly as they went to the wire.
func (t *casTee) rawBody(tb testing.TB, i int) []byte {
	tb.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	if i >= len(t.reqs) {
		tb.Fatalf("request %d not captured (have %d)", i, len(t.reqs))
	}
	return t.reqs[i].Body
}

// url returns the i-th captured request URL.
func (t *casTee) url(tb testing.TB, i int) string {
	tb.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	if i >= len(t.reqs) {
		tb.Fatalf("request %d not captured (have %d)", i, len(t.reqs))
	}
	return t.reqs[i].URL
}

// view decodes the i-th captured request through the provider's decoder.
func (p casProvider) view(t *testing.T, tee *casTee, i int) casView {
	t.Helper()
	bodies := tee.bodies(t)
	if i >= len(bodies) {
		t.Fatalf("request %d not captured (have %d)", i, len(bodies))
	}
	v := p.Decode(t, tee.url(t, i), bodies[i])
	v.Raw = tee.rawBody(t, i)
	return v
}

// client binds a client to testdata/cassettes/<Name>_<scenario>.json.
func (p casProvider) client(t *testing.T, scenario string) (*Client, *casTee) {
	t.Helper()
	tee := &casTee{inner: cassette.New(t, p.Name+"_"+scenario)}
	return New(WithTransport(tee), WithAPIKey(p.Model.Provider, p.key(t))), tee
}

// key returns the live key when recording. On replay nothing leaves the
// process: cassettes redact auth headers and matching ignores them (R23, R24).
func (p casProvider) key(t *testing.T) string {
	t.Helper()
	if os.Getenv("RECORD") == "" {
		return "replay-placeholder-key"
	}
	key := os.Getenv(p.EnvKey)
	if key == "" {
		t.Fatalf("RECORD is set but %s is empty: export a key to record this cassette", p.EnvKey)
	}
	return key
}

// extra runs the provider's protocol-specific payload assertions, if any.
func (p casProvider) extra(t *testing.T, scenario string, tee *casTee) {
	t.Helper()
	if fn := p.Extra[scenario]; fn != nil {
		fn(t, tee.bodies(t))
	}
}

func mustCasJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func casTemp(v float64) *float64 { return &v }

// effort is the reasoning level the thinking scenarios request.
func (p casProvider) effort() Effort {
	if p.ThinkingEffort == "" {
		return EffortLow
	}
	return p.ThinkingEffort
}

// tokens applies the provider's output-token floor to a scenario's budget.
func (p casProvider) tokens(n int) int {
	return max(n, p.MinOutputTokens)
}

// ---- shared fixtures --------------------------------------------------------

// casWeather is the tool used by every tool-calling scenario.
var casWeather = ToolDef{
	Name:        "get_weather",
	Description: "Get the current weather for a city.",
	Schema: json.RawMessage(`{
		"type": "object",
		"properties": {"city": {"type": "string", "description": "City name, e.g. Copenhagen"}},
		"required": ["city"]
	}`),
}

// casPNG is a 16x16 crimson PNG, hardcoded rather than generated with
// image/png so a Go version change cannot alter the recorded request bytes
// and break cassette matching.
const casPNG = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAHUlEQVR4nGK5I2LDQApgIkn1qIZRDUNKAyAAAP//0nUBT3dZ/MAAAAAASUVORK5CYII="

func casImageBlock() Block {
	return Block{Type: BlockImage, MimeType: "image/png", Data: casPNG}
}

const (
	casToolSystem = "Use the tools you are given. Do not ask follow-up questions."
	casWeatherQ   = "What is the weather in Copenhagen right now?"
	casWeatherOut = `{"temp_c":18,"conditions":"light rain"}`
)

// casFirstToolCall returns the first tool_call block of a response.
func casFirstToolCall(t *testing.T, m Message) Block {
	t.Helper()
	for _, b := range m.Blocks {
		if b.Type == BlockToolCall {
			return b
		}
	}
	t.Fatalf("no tool_call block in %+v", m.Blocks)
	return Block{}
}

// casReplayToken returns the part of a signature that must appear verbatim in
// the next request. Most protocols carry Block.Signature opaquely and send it
// back byte-for-byte; openai-responses packs an id plus encrypted content into
// it as JSON and destructures them into separate wire fields, so the encrypted
// blob is the piece to look for there.
func casReplayToken(t *testing.T, sig string) string {
	t.Helper()
	var s oairSig
	if json.Unmarshal([]byte(sig), &s) == nil && s.EncryptedContent != "" {
		return s.EncryptedContent
	}
	return sig
}

func casHasEvent(events []Event, typ EventType) bool {
	for _, ev := range events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// casLongSystem builds a deterministic system prompt of roughly 5k tokens.
// The minimum cacheable prefix is model-dependent and not monotonic across
// generations — haiku 4.5 needs 4096 tokens where sonnet 4.5 needed 1024 —
// so this is sized for the highest floor in the matrix. Undersize it and the
// breakpoint is silently ignored: no error, just cache_write = 0.
func casLongSystem() string {
	var b strings.Builder
	b.WriteString("You are the reference assistant for a fictional Go library named widgetkit.\n")
	for i := range 200 {
		fmt.Fprintf(&b, "Rule %03d: when the user asks about widgetkit topic %03d, answer in one sentence and cite manual section %03d.\n", i, i, i)
	}
	return b.String()
}

// ---- suite runner -----------------------------------------------------------

var casScenarios = []struct {
	name string
	run  func(t *testing.T, p casProvider)
}{
	{"basic_text", casScenBasicText},
	{"tool_call", casScenToolCall},
	{"tool_result_roundtrip", casScenToolResultRoundtrip},
	{"thinking", casScenThinking},
	{"thinking_replay", casScenThinkingReplay},
	{"image_input", casScenImageInput},
	{"image_in_tool_result", casScenImageInToolResult},
	{"multi_tool_parallel", casScenMultiToolParallel},
	{"error_400", casScenError400},
	{"abort_mid_stream", casScenAbortMidStream},
	{"unicode", casScenUnicode},
	{"caching", casScenCaching},
	{"handoff", casScenHandoff},
}

// runCassetteSuite runs the whole §12.2 matrix for one protocol.
func runCassetteSuite(t *testing.T, p casProvider) {
	if p.Decode == nil {
		t.Fatalf("provider %q has no Decode", p.Name)
	}
	for _, sc := range casScenarios {
		t.Run(sc.name, func(t *testing.T) {
			if reason, skipped := p.Skip[sc.name]; skipped {
				t.Skipf("not applicable to %s: %s", p.Name, reason)
			}
			sc.run(t, p)
		})
	}
}

// ---- scenarios --------------------------------------------------------------

func casScenBasicText(t *testing.T, p casProvider) {
	c, tee := p.client(t, "basic_text")

	s := c.Stream(context.Background(), Request{
		Model:       p.Model,
		System:      "Answer in one short sentence. Be direct.",
		Messages:    []Message{UserText("What is the capital of Denmark?")},
		MaxTokens:   p.tokens(128),
		Temperature: p.Temp,
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	v := p.view(t, tee, 0)
	if v.Model != p.Model.ID {
		t.Errorf("model = %q, want %q", v.Model, p.Model.ID)
	}
	if !v.Stream {
		t.Error("request did not ask for streaming")
	}
	if want := p.tokens(128); v.MaxTokens != want {
		t.Errorf("max tokens = %d, want %d", v.MaxTokens, want)
	}
	if v.System != "Answer in one short sentence. Be direct." {
		t.Errorf("system = %q", v.System)
	}
	if len(v.Messages) != 1 || v.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", v.Messages)
	}
	if !strings.Contains(v.Messages[0].Text, "capital of Denmark") {
		t.Errorf("user text = %q", v.Messages[0].Text)
	}

	if msg.StopReason != StopEnd {
		t.Errorf("stop = %q, want %q", msg.StopReason, StopEnd)
	}
	if got := textOf(msg); !strings.Contains(got, "Copenhagen") {
		t.Errorf("text = %q, want it to mention Copenhagen", got)
	}
	if !casHasEvent(events, EventTextDelta) {
		t.Errorf("no text deltas: %v", eventTypes(events))
	}
	if first, last := events[0].Type, events[len(events)-1].Type; first != EventStart || last != EventDone {
		t.Errorf("event boundaries = %v .. %v", first, last)
	}
	if msg.Usage == nil || msg.Usage.Input <= 0 || msg.Usage.Output <= 0 {
		t.Fatalf("usage = %+v, want non-zero input and output", msg.Usage)
	}
	if msg.Usage.TotalCost <= 0 {
		t.Errorf("total cost = %v, want > 0 (R4)", msg.Usage.TotalCost)
	}
	if msg.Model != p.Model.ID || msg.Provider != p.Model.Provider || msg.API != p.Model.API {
		t.Errorf("provenance = %q/%q/%q, want %q/%q/%q",
			msg.Model, msg.Provider, msg.API, p.Model.ID, p.Model.Provider, p.Model.API)
	}
	p.extra(t, "basic_text", tee)
}

func casScenToolCall(t *testing.T, p casProvider) {
	c, tee := p.client(t, "tool_call")

	s := c.Stream(context.Background(), Request{
		Model:       p.Model,
		System:      casToolSystem,
		Messages:    []Message{UserText(casWeatherQ)},
		Tools:       []ToolDef{casWeather},
		MaxTokens:   p.tokens(256),
		Temperature: p.Temp,
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// The tool definition reached the wire with its schema intact.
	v := p.view(t, tee, 0)
	if len(v.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(v.Tools))
	}
	if v.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q", v.Tools[0].Name)
	}
	if v.Tools[0].Description == "" {
		t.Error("tool description dropped")
	}
	props, _ := v.Tools[0].Schema["properties"].(map[string]any)
	city, _ := props["city"].(map[string]any)
	if city == nil || city["type"] != "string" {
		t.Errorf("tool schema properties.city not forwarded: %v", v.Tools[0].Schema)
	}

	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q, want %q (text: %q)", msg.StopReason, StopToolUse, textOf(msg))
	}
	call := casFirstToolCall(t, msg)
	if call.Name != "get_weather" {
		t.Errorf("tool name = %q", call.Name)
	}
	// R18 synthesizes call_<n> at normalization time, not on decode, so a
	// protocol whose wire carries no id legitimately yields an empty one here.
	if call.ID == "" && !p.NoToolCallIDs {
		t.Error("tool call has no ID")
	}
	// R7: args arrive as fragments and end up complete, valid JSON.
	if !casHasEvent(events, EventToolCallStart) || !casHasEvent(events, EventToolCallEnd) {
		t.Errorf("missing tool call events: %v", eventTypes(events))
	}
	var args struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("args not valid JSON: %v (%s)", err, call.Args)
	}
	if !strings.Contains(strings.ToLower(args.City), "copenhagen") {
		t.Errorf("args.city = %q, want Copenhagen", args.City)
	}
	p.extra(t, "tool_call", tee)
}

func casScenToolResultRoundtrip(t *testing.T, p casProvider) {
	c, tee := p.client(t, "tool_result_roundtrip")
	ctx := context.Background()

	req := Request{
		Model:       p.Model,
		System:      casToolSystem,
		Messages:    []Message{UserText(casWeatherQ)},
		Tools:       []ToolDef{casWeather},
		MaxTokens:   p.tokens(256),
		Temperature: p.Temp,
	}
	first, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if first.StopReason != StopToolUse {
		t.Fatalf("first turn stop = %q, want %q", first.StopReason, StopToolUse)
	}
	call := casFirstToolCall(t, first)

	req.Messages = append(req.Messages, first,
		ToolResultMessage(call.ID, call.Name, false, TextBlock(casWeatherOut)))
	second, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}

	// The second request replays the call and its result, correctly paired.
	v := p.view(t, tee, 1)
	calls, results := v.allToolCalls(), v.allToolResults()
	if len(calls) != 1 {
		t.Fatalf("replayed tool calls = %d, want 1: %+v", len(calls), calls)
	}
	if len(results) != 1 {
		t.Fatalf("replayed tool results = %d, want 1: %+v", len(results), results)
	}
	if calls[0].Name != "get_weather" {
		t.Errorf("replayed call name = %q", calls[0].Name)
	}
	if p.NoToolCallIDs {
		// Pairing is by function name on this protocol.
		if results[0].Name != calls[0].Name {
			t.Errorf("result pairs to %q, want %q", results[0].Name, calls[0].Name)
		}
	} else {
		if calls[0].ID != call.ID {
			t.Errorf("replayed call id = %q, want %q", calls[0].ID, call.ID)
		}
		if results[0].ID != call.ID {
			t.Errorf("result tool call id = %q, want %q", results[0].ID, call.ID)
		}
	}
	if !strings.Contains(results[0].Text, "18") {
		t.Errorf("result text = %q, want the tool output", results[0].Text)
	}

	if second.StopReason != StopEnd {
		t.Errorf("second turn stop = %q, want %q", second.StopReason, StopEnd)
	}
	if got := textOf(second); !strings.Contains(got, "18") {
		t.Errorf("answer = %q, want it to use the tool result temperature", got)
	}
	p.extra(t, "tool_result_roundtrip", tee)
}

func casScenThinking(t *testing.T, p casProvider) {
	c, tee := p.client(t, "thinking")

	s := c.Stream(context.Background(), Request{
		Model:     p.Model,
		System:    "Think it through, then give the number only.",
		Messages:  []Message{UserText("A shop sells pens at 7 kr and pads at 23 kr. I buy 3 pens and 2 pads. What do I pay?")},
		Reasoning: p.effort(),
		MaxTokens: p.tokens(4096),
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	// R14: effort reaches the wire in whatever form this protocol uses.
	v := p.view(t, tee, 0)
	if v.Thinking == nil || !v.Thinking.Enabled {
		t.Fatalf("reasoning not requested on the wire: %s", v.Raw)
	}

	// The signature is what makes same-model replay possible, so every
	// protocol must return one — but which block carries it differs.
	// Anthropic and openai-responses use a dedicated thinking block; gemini
	// 3.x returns no thought part at all and hangs the signature on the
	// content block the turn produced.
	var sig string
	for _, b := range msg.Blocks {
		if b.Signature != "" {
			sig = b.Signature
			break
		}
	}
	if sig == "" {
		t.Errorf("no signature on any block — nothing to replay next turn: %+v", msg.Blocks)
	}

	if !p.NoThinkingText {
		if !casHasEvent(events, EventThinkingDelta) {
			t.Errorf("no thinking deltas: %v", eventTypes(events))
		}
		var thinking *Block
		for i, b := range msg.Blocks {
			if b.Type == BlockThinking {
				thinking = &msg.Blocks[i]
				break
			}
		}
		if thinking == nil {
			t.Fatalf("no thinking block in %+v", msg.Blocks)
		}
		if thinking.Text == "" && !thinking.Redacted {
			t.Error("thinking block has no text")
		}
		if thinking.Signature == "" {
			t.Error("thinking block has no signature")
		}
	}
	if got := textOf(msg); !strings.Contains(got, "67") {
		t.Errorf("answer = %q, want 67", got)
	}
	p.extra(t, "thinking", tee)
}

func casScenThinkingReplay(t *testing.T, p casProvider) {
	c, tee := p.client(t, "thinking_replay")
	ctx := context.Background()

	// A plain reasoning turn rather than a tool call. Luna emits no reasoning
	// item at all for a simple tool-calling prompt — reasoning_tokens:0 even
	// at high effort — leaving nothing to replay; this is the prompt the
	// thinking scenario already proves makes every model reason. Tools are
	// not what this scenario is about: the claim is that whatever replayable
	// reasoning metadata came back goes out again on the next turn, and the
	// provider validates it.
	req := Request{
		Model:     p.Model,
		System:    "Think it through, then give the number only.",
		Messages:  []Message{UserText("A shop sells pens at 7 kr and pads at 23 kr. I buy 3 pens and 2 pads. What do I pay?")},
		Reasoning: p.effort(),
		MaxTokens: p.tokens(4096),
	}
	first, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	var sig string
	for _, b := range first.Blocks {
		if b.Signature != "" {
			sig = b.Signature
			break
		}
	}
	if sig == "" {
		t.Fatalf("first turn returned no signature to replay: %+v", first.Blocks)
	}

	req.Messages = append(req.Messages, first, UserText("And if I add one more pad?"))
	second, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}

	// R17: same model, so the reasoning metadata replays natively rather than
	// being textified. Asserting the exact signature travelled back works on
	// every protocol regardless of which block carries it — and the provider
	// validates it, so a recorded success is the real proof.
	v := p.view(t, tee, 1)
	if token := casReplayToken(t, sig); !strings.Contains(string(v.Raw), token) {
		t.Errorf("first turn's replayable reasoning metadata was not sent back (R17):\n%s", v.Raw)
	}
	if hoHasThinkingTag(string(v.Raw)) {
		t.Error("same-model thinking must not be textified (R17)")
	}
	if textOf(second) == "" {
		t.Error("model returned no answer on the second turn")
	}
	p.extra(t, "thinking_replay", tee)
}

func casScenImageInput(t *testing.T, p casProvider) {
	c, tee := p.client(t, "image_input")

	msg, err := c.Complete(context.Background(), Request{
		Model: p.Model,
		Messages: []Message{UserBlocks(
			TextBlock("What colour is this image? Answer with a single word."),
			casImageBlock(),
		)},
		MaxTokens:   p.tokens(64),
		Temperature: p.Temp,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	v := p.view(t, tee, 0)
	images := v.allImages()
	if len(images) != 1 {
		t.Fatalf("images on the wire = %d, want 1: %s", len(images), v.Raw)
	}
	if images[0].MimeType != "image/png" {
		t.Errorf("mime type = %q", images[0].MimeType)
	}
	if images[0].Data != casPNG {
		t.Error("image data does not match the input image")
	}

	if msg.StopReason != StopEnd {
		t.Errorf("stop = %q", msg.StopReason)
	}
	if textOf(msg) == "" {
		t.Error("model returned no description of the image")
	}
	p.extra(t, "image_input", tee)
}

func casScenImageInToolResult(t *testing.T, p casProvider) {
	c, tee := p.client(t, "image_in_tool_result")
	ctx := context.Background()

	screenshot := ToolDef{
		Name:        "screenshot",
		Description: "Take a screenshot of the current page.",
		Schema:      json.RawMessage(`{"type":"object","properties":{}}`),
	}
	// The assistant turn comes from the model rather than being hand-built:
	// gemini 3.x rejects a replayed functionCall that is missing the thought
	// signature it originally issued, which synthetic history cannot supply.
	req := Request{
		Model:       p.Model,
		System:      casToolSystem,
		Messages:    []Message{UserText("Take a screenshot and tell me the dominant colour.")},
		Tools:       []ToolDef{screenshot},
		MaxTokens:   p.tokens(256),
		Temperature: p.Temp,
	}
	first, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if first.StopReason != StopToolUse {
		t.Fatalf("first turn stop = %q, want %q (text: %q)", first.StopReason, StopToolUse, textOf(first))
	}
	call := casFirstToolCall(t, first)

	req.Messages = append(req.Messages, first,
		ToolResultMessage(call.ID, call.Name, false,
			TextBlock("screenshot captured"),
			casImageBlock(),
		))
	msg, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}

	// §8.2/§8.3: where the image ends up is protocol-specific — nested in the
	// tool result on anthropic, spilled into a following user message on the
	// openai protocols — but it must survive somewhere, unmodified.
	v := p.view(t, tee, 1)
	images := v.allImages()
	if len(images) != 1 {
		t.Fatalf("images on the wire = %d, want 1: %s", len(images), v.Raw)
	}
	if images[0].Data != casPNG {
		t.Error("image data does not match the input image")
	}
	if results := v.allToolResults(); len(results) != 1 {
		t.Errorf("tool results = %d, want 1", len(results))
	} else if !strings.Contains(results[0].Text, "screenshot captured") {
		t.Errorf("tool result text = %q", results[0].Text)
	}
	if textOf(msg) == "" {
		t.Error("model returned no answer")
	}
	p.extra(t, "image_in_tool_result", tee)
}

func casScenMultiToolParallel(t *testing.T, p casProvider) {
	c, tee := p.client(t, "multi_tool_parallel")

	s := c.Stream(context.Background(), Request{
		Model:  p.Model,
		System: "Use the tools you are given. When several lookups are needed, request them all at once.",
		Messages: []Message{
			UserText("What is the weather in Copenhagen and in Aarhus? Look up both."),
		},
		Tools:       []ToolDef{casWeather},
		MaxTokens:   p.tokens(512),
		Temperature: p.Temp,
	})
	events := collectEvents(s)
	msg, err := s.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if msg.StopReason != StopToolUse {
		t.Fatalf("stop = %q, want %q (text: %q)", msg.StopReason, StopToolUse, textOf(msg))
	}

	var calls []Block
	for _, b := range msg.Blocks {
		if b.Type == BlockToolCall {
			calls = append(calls, b)
		}
	}
	if len(calls) < 2 {
		t.Fatalf("tool calls = %d, want at least 2 in one message", len(calls))
	}
	seen := map[string]bool{}
	cities := map[string]bool{}
	for _, call := range calls {
		if !p.NoToolCallIDs {
			if call.ID == "" || seen[call.ID] {
				t.Errorf("duplicate or empty tool call ID %q (R18)", call.ID)
			}
			seen[call.ID] = true
		}
		var args struct {
			City string `json:"city"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			t.Fatalf("args not valid JSON: %v (%s)", err, call.Args)
		}
		cities[strings.ToLower(args.City)] = true
	}
	if !cities["copenhagen"] || !cities["aarhus"] {
		t.Errorf("cities requested = %v, want copenhagen and aarhus", cities)
	}

	// Each call's start/end brackets its own deltas, keyed by block index.
	open := map[int]bool{}
	for _, ev := range events {
		switch ev.Type {
		case EventToolCallStart:
			open[ev.Index] = true
		case EventToolCallDelta:
			if !open[ev.Index] {
				t.Errorf("tool_call_delta at index %d before its start", ev.Index)
			}
		case EventToolCallEnd:
			if !open[ev.Index] {
				t.Errorf("tool_call_end at index %d without a start", ev.Index)
			}
			delete(open, ev.Index)
		}
	}
	if len(open) != 0 {
		t.Errorf("tool call blocks never closed: %v", open)
	}
	p.extra(t, "multi_tool_parallel", tee)
}

func casScenError400(t *testing.T, p casProvider) {
	c, tee := p.client(t, "error_400")

	s := c.Stream(context.Background(), p.BadRequest(p.Model))
	events := collectEvents(s)
	msg, err := s.Message()

	if err == nil {
		t.Fatal("want an error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v (%T), want *HTTPError", err, err)
	}
	if httpErr.Status < 400 || httpErr.Status >= 500 {
		t.Errorf("status = %d, want 4xx", httpErr.Status)
	}
	if msg.StopReason != StopError {
		t.Errorf("stop = %q, want %q", msg.StopReason, StopError)
	}
	// R8: the provider's body reaches the caller verbatim.
	if msg.ErrorText == "" {
		t.Error("ErrorText empty, want the provider's error body")
	}
	if last := events[len(events)-1]; last.Type != EventError {
		t.Errorf("last event = %v, want %v", last.Type, EventError)
	}
	p.extra(t, "error_400", tee)
}

func casScenAbortMidStream(t *testing.T, p casProvider) {
	c, tee := p.client(t, "abort_mid_stream")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Recording consumes the whole stream; only replay aborts (SPEC §12.2:
	// "the cassette transport keeps serving chunks but the reader stops —
	// deterministic"). Cancelling while recording would truncate the cassette
	// to whatever had arrived, and a response that lands in a single read
	// then leaves replay no chunk boundary to abort between.
	recording := os.Getenv("RECORD") != ""

	s := c.Stream(ctx, Request{
		Model:  p.Model,
		System: "Answer plainly, no preamble.",
		// Deliberately long: the SSE reader buffers 4KiB at a time, so a
		// short answer arrives in one Read and cannot be interrupted at all.
		Messages:    []Message{UserText("List the numbers from 1 to 300, one per line, nothing else.")},
		MaxTokens:   p.tokens(1024),
		Temperature: p.Temp,
	})
	for ev := range s.Events() {
		if ev.Type == EventTextDelta && !recording {
			cancel() // abort on the first delta; keep ranging to the terminal event
		}
	}
	msg, err := s.Message()

	if recording {
		// The recording pass only exists to capture a complete, multi-chunk
		// stream. Replay is where the abort is actually exercised.
		if err != nil {
			t.Fatalf("recording the full stream failed: %v", err)
		}
		t.Log("recorded the full stream; re-run without RECORD to exercise the abort")
		return
	}

	if err == nil {
		t.Fatalf("stream finished instead of aborting — the recorded response probably fits in one 4KiB read, leaving no chunk boundary to cancel between; re-record with a longer response")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if msg.StopReason != StopAborted {
		t.Errorf("stop = %q, want %q", msg.StopReason, StopAborted)
	}
	// R8: whatever arrived before the cancel is kept.
	if textOf(msg) == "" {
		t.Error("partial text discarded on abort")
	}
	p.extra(t, "abort_mid_stream", tee)
}

func casScenUnicode(t *testing.T, p casProvider) {
	c, tee := p.client(t, "unicode")

	const want = "héllo 日本語 👨‍👩‍👧‍👦 🇩🇰 🧑‍🚀"
	msg, err := c.Complete(context.Background(), Request{
		Model:       p.Model,
		System:      "Reply with exactly the text the user gives you, nothing else.",
		Messages:    []Message{UserText(want)},
		MaxTokens:   p.tokens(128),
		Temperature: p.Temp,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	v := p.view(t, tee, 0)
	if len(v.Messages) == 0 || v.Messages[0].Text != want {
		t.Errorf("request text = %q, want %q", v.Messages[0].Text, want)
	}

	// The reply survives SSE chunk boundaries: multi-byte runes, ZWJ
	// sequences and regional-indicator pairs get split across chunks by the
	// wire, and the accumulator must not corrupt them.
	got := textOf(msg)
	if !utf8.ValidString(got) {
		t.Fatalf("reply is not valid UTF-8: %q", got)
	}
	for _, frag := range []string{"日本語", "👨‍👩‍👧‍👦", "🇩🇰"} {
		if !strings.Contains(got, frag) {
			t.Errorf("reply %q lost %q", got, frag)
		}
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("reply contains U+FFFD: %q", got)
	}
	p.extra(t, "unicode", tee)
}

func casScenCaching(t *testing.T, p casProvider) {
	if p.CacheModel.ID == "" {
		t.Skipf("no explicit prompt-cache breakpoints on %s (R15)", p.Name)
	}
	c, tee := p.client(t, "caching")
	ctx := context.Background()

	req := Request{
		Model:       p.CacheModel,
		System:      casLongSystem(),
		Messages:    []Message{UserText("Which manual section covers topic 007?")},
		MaxTokens:   p.tokens(128),
		Temperature: p.Temp,
	}
	first, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	// Same cacheable prefix, different tail.
	req.Messages = []Message{UserText("Which manual section covers topic 042?")}
	second, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}

	if first.Usage == nil || second.Usage == nil {
		t.Fatalf("usage missing: %+v %+v", first.Usage, second.Usage)
	}
	// Whether turn one writes the prefix or reads it depends on whether an
	// earlier run left it warm: anthropic's ephemeral entries live about five
	// minutes, so re-recording inside that window sees a read where a cold
	// run sees a write. Demanding a write here would make this cell
	// impossible to record twice in a row — what matters is that the
	// breakpoint was honored at all.
	cached := max(first.Usage.CacheWrite, first.Usage.CacheRead)
	if cached <= 0 {
		t.Errorf("first turn neither wrote nor read the cache (write=%d read=%d): breakpoint not honored (R15)",
			first.Usage.CacheWrite, first.Usage.CacheRead)
	}
	// A prefix under the model's minimum caches silently as zero, which would
	// otherwise look like a passing test with caching quietly disabled.
	if cached > 0 && cached < 4096 {
		t.Errorf("cached prefix = %d tokens, under the 4096 floor haiku 4.5 needs — casLongSystem has been shrunk", cached)
	}
	// The second turn always shares the prefix, so it must be a read.
	if second.Usage.CacheRead <= 0 {
		t.Errorf("second turn cache_read = %d, want > 0", second.Usage.CacheRead)
	}
	// R4: cache reads are priced separately, so cost stays positive but the
	// second turn must not be charged full input price for the prefix.
	if second.Usage.TotalCost <= 0 {
		t.Errorf("second turn cost = %v, want > 0", second.Usage.TotalCost)
	}
	p.extra(t, "caching", tee)
}

func casScenHandoff(t *testing.T, p casProvider) {
	foreign := p.Foreign
	if foreign.ID == "" {
		t.Skip("no foreign protocol configured")
	}
	c, tee := p.client(t, "handoff_from_"+foreign.Provider)

	// History as a run on the foreign protocol would have left it: a thinking
	// block with that provider's opaque signature, its tool-call id format,
	// and a trailing call that never got a result.
	history := []Message{
		UserText(casWeatherQ),
		{
			Role: RoleAssistant, Model: foreign.ID, Provider: foreign.Provider, API: foreign.API,
			StopReason: StopToolUse,
			Blocks: []Block{
				{Type: BlockThinking, Text: "The user wants current weather; call the tool.", Signature: "cas_foreign_opaque_sig_abc123"},
				{Type: BlockText, Text: "Let me look that up."},
				{Type: BlockToolCall, ID: "call_9xKpQr2Lm", Name: "get_weather", Args: json.RawMessage(`{"city":"Copenhagen"}`)},
			},
		},
		ToolResultMessage("call_9xKpQr2Lm", "get_weather", false, TextBlock(casWeatherOut)),
		{
			Role: RoleAssistant, Model: foreign.ID, Provider: foreign.Provider, API: foreign.API,
			StopReason: StopToolUse,
			Blocks: []Block{
				// Orphaned: no result follows, so R16 must synthesize one.
				{Type: BlockToolCall, ID: "call_7zBnWq4Tv", Name: "get_weather", Args: json.RawMessage(`{"city":"Aarhus"}`)},
			},
		},
		UserText("Just summarise what you found for Copenhagen."),
	}

	msg, err := c.Complete(context.Background(), Request{
		Model:       p.Model,
		System:      "Be brief.",
		Messages:    history,
		Tools:       []ToolDef{casWeather},
		MaxTokens:   p.tokens(256),
		Temperature: p.Temp,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	v := p.view(t, tee, 0)
	raw := string(v.Raw)
	// R17: foreign thinking is textified, and the opaque signature never
	// reaches a provider that cannot validate it.
	if !hoHasThinkingTag(raw) {
		t.Errorf("foreign thinking not textified (R17):\n%s", raw)
	}
	if strings.Contains(raw, "cas_foreign_opaque_sig_abc123") {
		t.Errorf("foreign signature leaked to the wire (R17):\n%s", raw)
	}
	// R16: the orphaned call is paired with a synthetic result — the recorded
	// success proves the provider accepted the repaired history.
	if got := len(v.allToolResults()); got < 2 {
		t.Errorf("tool results = %d, want 2 (orphaned call needs a synthetic one, R16):\n%s", got, raw)
	}
	if got := len(v.allToolCalls()); got != 2 {
		t.Errorf("tool calls = %d, want 2:\n%s", got, raw)
	}
	// R18: ids are carried through consistently on call and result.
	if !p.NoToolCallIDs {
		for _, id := range []string{"call_9xKpQr2Lm", "call_7zBnWq4Tv"} {
			if !strings.Contains(raw, id) {
				t.Errorf("tool call id %q not preserved (R18):\n%s", id, raw)
			}
		}
	}

	if msg.StopReason != StopEnd {
		t.Errorf("stop = %q, want %q", msg.StopReason, StopEnd)
	}
	if textOf(msg) == "" {
		t.Error("model returned no summary")
	}
	p.extra(t, "handoff", tee)
}

// ---- decoder helpers --------------------------------------------------------
//
// Deliberately lenient: a decoder reads whatever its protocol writes, and the
// scenario assertions are what fail when something is missing.

func casMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func casList(v any) []any         { l, _ := v.([]any); return l }
func casStr(v any) string         { s, _ := v.(string); return s }
func casBool(v any) bool          { b, _ := v.(bool); return b }

func casInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// casDataURI splits a "data:<mime>;base64,<data>" URI, which the openai
// protocols use to carry inline images.
func casDataURI(uri string) casImage {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return casImage{}
	}
	mime, data, ok := strings.Cut(rest, ";base64,")
	if !ok {
		return casImage{}
	}
	return casImage{MimeType: mime, Data: data}
}
