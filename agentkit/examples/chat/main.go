// Command chat is a minimal streaming chat against the low-level llm layer:
// no session, no tools — just a message slice and one streamed completion
// per user input.
//
// Requires ANTHROPIC_API_KEY.
//
//	go run ./examples/chat
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/oliverkofoed/gokit/agentkit/llm"
)

func main() {
	client := llm.New() // transport.HTTP(), API key from ANTHROPIC_API_KEY
	model := llm.ClaudeSonnet45

	var messages []llm.Message

	fmt.Printf("chatting with %s — ctrl-d to exit\n", model.ID)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		messages = append(messages, llm.UserText(text))

		stream := client.Stream(context.Background(), llm.Request{
			Model:    model,
			System:   "You are a concise assistant. Answer in plain text.",
			Messages: messages,
		})

		for ev := range stream.Events() {
			if ev.Type == llm.EventTextDelta {
				fmt.Print(ev.Delta)
			}
		}

		msg, err := stream.Message()
		if err != nil {
			// Partial content is preserved on msg, but for a simple chat we
			// drop the failed exchange so the next turn starts clean.
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
			messages = messages[:len(messages)-1]
			continue
		}
		messages = append(messages, msg)

		if u := msg.Usage; u != nil {
			fmt.Printf("\n  (%d in · %d out · $%.4f)\n", u.Input, u.Output, u.TotalCost)
		}
	}
}
