# agentkit

A small Go library for building agent loops — especially coding agents — on
top of LLM providers. Seven primitives, standard library only, every byte of
network traffic replayable in tests.

```
go get github.com/oliverkofoed/gokit/agentkit
```

```go
session := agentkit.New(agentkit.Config{
    LLM:    llm.New(), // keys from env: ANTHROPIC_API_KEY, OPENAI_API_KEY, ...
    Model:  llm.ClaudeSonnet45,
    System: "You are a careful coding agent.",
    Tools:  []agentkit.Tool{readFile, writeFile, runCommand},
})

for ev := range session.Run(ctx, "add a --verbose flag to cmd/serve") {
    switch ev.Type {
    case agentkit.EventModel:
        if ev.Stream.Type == llm.EventTextDelta {
            fmt.Print(ev.Stream.Delta)
        }
    case agentkit.EventToolStart:
        fmt.Printf("\n⚒ %s %s\n", ev.Call.Name, ev.Call.Args)
    }
}
```

## What it does

- **Four wire protocols, no SDKs**: Anthropic Messages, OpenAI Responses,
  OpenAI Chat Completions (OpenRouter, Groq, Ollama, vLLM, …), Google Gemini —
  implemented directly on `net/http` behind one transport seam.
- **Models are config**, not a catalog: an `llm.Model` is a dozen JSON-serializable
  fields; a few well-known shorthands ship (`llm.ClaudeSonnet45`, `llm.GPT5Mini`,
  `llm.OpenRouter("any/model")`).
- **Pull-based streaming**: runs and streams are `iter.Seq[Event]`; the loop
  advances as you consume, and breaking out interrupts cleanly.
- **Typed tools**: `agentkit.NewTool[Args]` derives the JSON Schema from your
  struct by reflection and validates arguments before your function runs.
- **Live sessions**: queue steering messages while the agent works
  (`session.Send`), queue follow-up work (`session.FollowUp`), hard-interrupt
  (`session.Interrupt`), and switch models mid-run (`session.SetModel`) with
  automatic cross-provider history handoff.
- **State is JSON**: `session.State()` marshals; `agentkit.Resume` continues —
  anywhere.
- **Reasoning, usage, cost, prompt caching**: uniform effort levels mapped per
  provider, token accounting with cache read/write, cost from the model's
  price table, automatic Anthropic cache breakpoints.

## Testing story

All traffic flows through `llm/transport.Interface`:

- **`llm/cassette`** records real provider traffic to JSON files (auth and
  account identity redacted, SSE chunk boundaries preserved) and replays them
  byte-for-byte. Re-record with `RECORD=1 go test`. Cassettes are committed, so
  **read a re-recorded cassette before committing it** — redaction covers
  credentials and identity headers, but prompts and completions are stored
  verbatim. `TestCassetteHygiene` fails the build on anything key-shaped.
- **`llm/llmtest`** is a scripted fake `Streamer` for deterministic
  agent-loop tests — word-level deltas, blockable replies, request capture.

`go test ./...` runs the whole suite offline with no keys.

## Layout

| Package | What |
|---|---|
| `agentkit` (root) | `Session` — the agent loop, tools, events |
| `agentkit/llm` | `Client`, `Model`, `Message`, `Stream` — stateless calls |
| `agentkit/llm/transport` | the only network seam |
| `agentkit/llm/cassette` | record/replay transport |
| `agentkit/llm/llmtest` | scripted fake |
| `examples/` | runnable examples: chat, coding agent, model switching |

The full specification — every public type and 37 numbered behavior rules the
test suite references — lives in [SPEC.md](SPEC.md).

## License

MIT
