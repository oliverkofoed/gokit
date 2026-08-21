// Command codingagent is a small coding agent: three typed tools, a policy
// hook, and the agentkit session loop. It runs one task from the command
// line against the current directory and streams what it does.
//
// Requires ANTHROPIC_API_KEY.
//
//	go run ./examples/codingagent "summarize what this project does"
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oliverkofoed/gokit/agentkit"
	"github.com/oliverkofoed/gokit/agentkit/llm"
)

// ---- tools ----------------------------------------------------------------

type readArgs struct {
	Path string `json:"path" jsonschema:"description=File path, relative to the working directory"`
}

var readFile = agentkit.NewTool("read_file", "Read a file's contents",
	func(ctx context.Context, a readArgs) (agentkit.ToolResult, error) {
		if !filepath.IsLocal(a.Path) {
			return agentkit.ToolResult{}, fmt.Errorf("path escapes the working directory: %s", a.Path)
		}
		b, err := os.ReadFile(a.Path)
		if err != nil {
			return agentkit.ToolResult{}, err
		}
		return agentkit.Text("%s", truncate(string(b), 50_000)), nil
	})

type writeArgs struct {
	Path    string `json:"path" jsonschema:"description=File path, relative to the working directory"`
	Content string `json:"content" jsonschema:"description=Full file content to write"`
}

var writeFile = agentkit.NewTool("write_file", "Create or overwrite a file",
	func(ctx context.Context, a writeArgs) (agentkit.ToolResult, error) {
		if !filepath.IsLocal(a.Path) {
			return agentkit.ToolResult{}, fmt.Errorf("path escapes the working directory: %s", a.Path)
		}
		if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
			return agentkit.ToolResult{}, err
		}
		if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
			return agentkit.ToolResult{}, err
		}
		return agentkit.Text("wrote %d bytes to %s", len(a.Content), a.Path), nil
	})

type bashArgs struct {
	Command string `json:"command" jsonschema:"description=Shell command to run in the working directory"`
}

var runCommand = agentkit.NewTool("bash", "Run a shell command and return its combined output",
	func(ctx context.Context, a bashArgs) (agentkit.ToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sh", "-c", a.Command).CombinedOutput()
		result := truncate(string(out), 50_000)
		if err != nil {
			// Non-zero exits are information for the model, not tool failures.
			return agentkit.Text("%s\n(command failed: %v)", result, err), nil
		}
		return agentkit.Text("%s", result), nil
	})

// ---- main -----------------------------------------------------------------

func main() {
	prompt := strings.Join(os.Args[1:], " ")
	if prompt == "" {
		prompt = "Give a one-paragraph summary of what this project is, based on the files here."
	}

	session := agentkit.New(agentkit.Config{
		LLM:       llm.New(),
		Model:     llm.ClaudeSonnet45,
		Reasoning: llm.EffortMedium,
		System: "You are a careful coding agent working in the current directory. " +
			"Prefer small, verifiable steps. Cite files as path:line.",
		Tools: []agentkit.Tool{readFile, writeFile, runCommand},

		// The policy seam: block anything scary before it executes. A denied
		// call becomes an is_error tool result the model can react to.
		BeforeTool: func(ctx context.Context, call agentkit.ToolCall) error {
			if call.Name == "bash" && strings.Contains(string(call.Args), "rm -rf") {
				return fmt.Errorf("blocked by policy: destructive command")
			}
			return nil
		},
	})

	for ev := range session.Run(context.Background(), prompt) {
		switch ev.Type {
		case agentkit.EventModel:
			if ev.Stream.Type == llm.EventTextDelta {
				fmt.Print(ev.Stream.Delta)
			}
		case agentkit.EventToolStart:
			fmt.Printf("\n⚒ %s %s\n", ev.Call.Name, compactArgs(ev.Call.Args))
		case agentkit.EventToolEnd:
			if ev.ToolErr != nil {
				fmt.Printf("  ✗ %v\n", ev.ToolErr)
			}
		case agentkit.EventRunEnd:
			fmt.Println()
			if ev.Err != nil {
				fmt.Fprintf(os.Stderr, "run failed: %v\n", ev.Err)
				os.Exit(1)
			}
		}
	}

	// Cost accounting comes straight off the persisted messages (R4).
	var cost float64
	for _, m := range session.State().Messages {
		if m.Usage != nil {
			cost += m.Usage.TotalCost
		}
	}
	fmt.Printf("\ntotal cost: $%.4f\n", cost)
}

func compactArgs(args json.RawMessage) string {
	return truncate(string(args), 120)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("… [%d bytes truncated]", len(s)-max)
}
