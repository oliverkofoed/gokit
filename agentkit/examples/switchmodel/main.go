// Command switchmodel demonstrates the two session superpowers: switching
// models mid-run (cross-provider handoff) and steering a run that is
// already in flight.
//
// Turn 1 runs on Claude. At the turn boundary we queue a steering message
// and switch the session to GPT-5 Mini — the steering message keeps the
// run alive, and turn 2 executes on the new model with the full history
// (Claude's thinking handed off per R17).
//
// Requires ANTHROPIC_API_KEY and OPENAI_API_KEY.
//
//	go run ./examples/switchmodel
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/oliverkofoed/gokit/agentkit"
	"github.com/oliverkofoed/gokit/agentkit/llm"
)

func main() {
	session := agentkit.New(agentkit.Config{
		LLM:       llm.New(),
		Model:     llm.ClaudeSonnet45,
		Reasoning: llm.EffortLow,
		System:    "You are a helpful assistant. Keep answers short.",
	})

	prompt := "Write a haiku about the Go programming language."

	for ev := range session.Run(context.Background(), prompt) {
		switch ev.Type {
		case agentkit.EventTurnStart:
			fmt.Printf("\n\n--- turn %d ---\n", ev.Turn)

		case agentkit.EventModel:
			if ev.Stream.Type == llm.EventTextDelta {
				fmt.Print(ev.Stream.Delta)
			}

		case agentkit.EventMessage:
			// Every completed assistant message records which model wrote it.
			if ev.Message.Role == llm.RoleAssistant {
				fmt.Printf("\n    ↳ produced by %s/%s", ev.Message.Provider, ev.Message.Model)
			}

		case agentkit.EventTurnEnd:
			if ev.Turn == 1 {
				// Both calls are safe from any goroutine at any time (R35);
				// here we simply make them at the turn boundary. The queued
				// steering message is what keeps the run going for turn 2,
				// which will build its request against the new model.
				session.SetModel(llm.GPT5Mini)
				session.Send("In one sentence: which model wrote that haiku, and which model are you?")
			}

		case agentkit.EventRunEnd:
			fmt.Println()
			if ev.Err != nil {
				fmt.Fprintf(os.Stderr, "run failed: %v\n", ev.Err)
				os.Exit(1)
			}
		}
	}
}
