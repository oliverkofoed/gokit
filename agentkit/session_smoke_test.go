package agentkit

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/oliverkofoed/gokit/agentkit/llm"
	"github.com/oliverkofoed/gokit/agentkit/llm/llmtest"
)

// TestSessionSmoke is a fast sanity check of the loop written alongside the
// core; the full suite lives in the dedicated session tests.
func TestSessionSmoke(t *testing.T) {
	type echoArgs struct {
		Text string `json:"text" jsonschema:"description=Text to echo"`
	}
	echo := NewTool("echo", "Echo text back",
		func(ctx context.Context, a echoArgs) (ToolResult, error) {
			return Text("echo: %s", a.Text), nil
		})

	fake := llmtest.New(
		llmtest.ToolCall("echo", map[string]string{"text": "hi"}),
		llmtest.Text("The tool said hi."),
	)
	s := New(Config{LLM: fake, Model: llm.ClaudeSonnet45, System: "sys", Tools: []Tool{echo}})

	var types []EventType
	var toolResultSeen bool
	for ev := range s.Run(context.Background(), "run echo") {
		types = append(types, ev.Type)
		if ev.Type == EventMessage && ev.Message.Role == llm.RoleToolResult {
			toolResultSeen = true
			if ev.Message.IsError {
				t.Fatalf("tool result errored: %v", ev.Message.Blocks)
			}
		}
		if ev.Type == EventRunEnd && ev.Err != nil {
			t.Fatalf("run failed: %v", ev.Err)
		}
	}
	if !toolResultSeen {
		t.Fatal("no tool result message")
	}
	if types[0] != EventRunStart || types[len(types)-1] != EventRunEnd {
		t.Fatalf("event boundaries wrong: %v", types)
	}

	// Two LLM calls; second saw the tool result in history.
	reqs := fake.Requests()
	if len(reqs) != 2 {
		t.Fatalf("llm calls = %d, want 2", len(reqs))
	}
	last := reqs[1].Messages[len(reqs[1].Messages)-1]
	if last.Role != llm.RoleToolResult || last.ToolName != "echo" {
		t.Fatalf("second request last message = %+v", last)
	}
	if reqs[0].System != "sys" || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "echo" {
		t.Fatalf("request shape: %+v", reqs[0])
	}

	// State snapshot round-trips and resumes.
	st := s.State()
	if len(st.Messages) != 4 { // user, assistant(tool_use), tool_result, assistant
		t.Fatalf("messages = %d, want 4", len(st.Messages))
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var st2 State
	if err := json.Unmarshal(b, &st2); err != nil {
		t.Fatal(err)
	}
	fake.Append(llmtest.Text("resumed fine"))
	s2 := Resume(Config{LLM: fake, Tools: []Tool{echo}}, st2)
	msg, err := Final(s2.Run(context.Background(), "and now?"))
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if msg.StopReason != llm.StopEnd {
		t.Fatalf("resumed stop = %q", msg.StopReason)
	}
}
