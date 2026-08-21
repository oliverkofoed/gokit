# Examples

Three small programs, one per layer of the public API. They were written
**before** the library (spec-first, against `SPEC.md`) and double as the API
contract: if a change to the library makes an example ugly, reconsider the
change. They are build-only checks — `go build ./agentkit/examples/...` keeps
them honest; running them needs real API keys.

| Example | Layer | Shows | Needs |
|---|---|---|---|
| [`chat`](chat/main.go) | `llm` | One streamed completion per input: `Client.Stream`, `Events()`, `Message()`, usage/cost on the final message. | `ANTHROPIC_API_KEY` |
| [`codingagent`](codingagent/main.go) | `agentkit` | Typed tools (`NewTool`), the session loop, `BeforeTool` policy blocking, event rendering, cost from `State()`. | `ANTHROPIC_API_KEY` |
| [`switchmodel`](switchmodel/main.go) | `agentkit` | Mid-run model switch (`SetModel`) + steering (`Send`) at a turn boundary; cross-provider handoff visible via `Message.Model`. | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` |

```bash
go run ./agentkit/examples/chat
go run ./agentkit/examples/codingagent "add a --verbose flag to cmd/serve"
go run ./agentkit/examples/switchmodel
```

Conventions the examples establish:

- **Consume runs with `for ev := range …` and a `switch ev.Type`.** Breaking
  out of the loop interrupts the run (SPEC §11.1).
- **Text deltas stream from `EventModel`/`EventTextDelta`;** completed
  messages arrive on `EventMessage` with provenance (`Model`, `Provider`)
  and `Usage` attached.
- **Tool failures are values.** Tools return errors for real failures; a
  command's non-zero exit is *content* for the model, not an error
  (`codingagent`'s `bash` tool).
- **Policy lives in `BeforeTool`,** not inside tools.
