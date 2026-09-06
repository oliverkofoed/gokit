package llm

import (
	"fmt"
	"regexp"
	"strings"
)

// Normalize applies history normalization (SPEC §5.3) to a copy of msgs, in
// order: Kind removal (R37), orphaned tool calls (R16), cross-model thinking
// handoff (R17), tool-call ID normalization (R18), empty content (R19),
// vision downgrade (R20), document downgrade (R38). The input is never
// mutated (R5).
//
// The Client applies it automatically before protocol encoding; it is
// exported for test doubles (llmtest records the normalized, as-sent view)
// and for context-building layers that want to preview what a model will see.
func Normalize(model Model, msgs []Message) []Message {
	return normalizeMessages(model, msgs)
}

func normalizeMessages(model Model, msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Kind != "" { // step 0: app-level messages never reach the wire (R37)
			continue
		}
		out = append(out, cloneMessage(m))
	}

	out = fixOrphanedToolCalls(out)
	out = handoffThinking(model, out)
	out = normalizeToolCallIDs(out)
	out = fixEmptyContent(out)
	if !model.Vision {
		out = downgradeImages(out)
	}
	if !model.Documents {
		out = downgradeDocuments(out)
	}
	return out
}

func cloneMessage(m Message) Message {
	c := m
	c.Blocks = make([]Block, len(m.Blocks))
	copy(c.Blocks, m.Blocks)
	for i := range c.Blocks {
		if len(c.Blocks[i].Args) > 0 {
			c.Blocks[i].Args = append([]byte(nil), c.Blocks[i].Args...)
		}
	}
	if len(m.Meta) > 0 {
		c.Meta = append([]byte(nil), m.Meta...)
	}
	if m.Usage != nil {
		u := *m.Usage
		c.Usage = &u
	}
	return c
}

// fixOrphanedToolCalls (R16): every tool_call block in an assistant message
// must be answered by a following tool_result. Unanswered calls get a
// synthetic error result injected after that assistant message's existing
// result cluster.
func fixOrphanedToolCalls(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		out = append(out, m)
		if m.Role != RoleAssistant {
			continue
		}
		var callIDs []string
		callNames := map[string]string{}
		for _, b := range m.Blocks {
			if b.Type == BlockToolCall {
				callIDs = append(callIDs, b.ID)
				callNames[b.ID] = b.Name
			}
		}
		if len(callIDs) == 0 {
			continue
		}
		// Consume the cluster of tool results that follows.
		answered := map[string]bool{}
		for i+1 < len(msgs) && msgs[i+1].Role == RoleToolResult {
			i++
			answered[msgs[i].ToolCallID] = true
			out = append(out, msgs[i])
		}
		for _, id := range callIDs {
			if !answered[id] {
				r := ToolResultMessage(id, callNames[id], true,
					TextBlock("tool call was interrupted; no result available"))
				r.Time = m.Time
				out = append(out, r)
			}
		}
	}
	return out
}

// handoffThinking (R17): assistant messages produced by a different model get
// thinking blocks converted to <thinking>-tagged text with signatures dropped;
// redacted thinking blocks are dropped entirely.
func handoffThinking(model Model, msgs []Message) []Message {
	for i, m := range msgs {
		if m.Role != RoleAssistant || m.Model == model.ID {
			continue
		}
		blocks := m.Blocks[:0]
		for _, b := range m.Blocks {
			if b.Type != BlockThinking {
				blocks = append(blocks, b)
				continue
			}
			if b.Redacted {
				continue
			}
			blocks = append(blocks, TextBlock("<thinking>\n"+b.Text+"\n</thinking>\n"))
		}
		msgs[i].Blocks = blocks
	}
	return msgs
}

var toolIDBad = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// normalizeToolCallIDs (R18): sanitize IDs to [a-zA-Z0-9_-]{1,40} with a
// consistent mapping across tool_call blocks and tool_result messages. Empty
// IDs (Gemini) become call_<n> by encounter order.
func normalizeToolCallIDs(msgs []Message) []Message {
	mapping := map[string]string{}
	taken := map[string]bool{}
	n := 0
	sanitize := func(id string) string {
		if mapped, ok := mapping[id]; ok {
			return mapped
		}
		s := toolIDBad.ReplaceAllString(id, "_")
		if len(s) > 40 {
			s = s[:40]
		}
		if s == "" {
			n++
			s = fmt.Sprintf("call_%d", n)
		}
		for taken[s] {
			n++
			s = fmt.Sprintf("%s_%d", trimTo(s, 36), n)
		}
		mapping[id] = s
		taken[s] = true
		return s
	}
	for i, m := range msgs {
		for j, b := range m.Blocks {
			if b.Type == BlockToolCall {
				msgs[i].Blocks[j].ID = sanitize(b.ID)
			}
		}
	}
	// Results map through the same table. A result whose call was never seen
	// keeps a sanitized form of its own ID.
	for i, m := range msgs {
		if m.Role == RoleToolResult {
			msgs[i].ToolCallID = sanitize(m.ToolCallID)
		}
	}
	return msgs
}

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// fixEmptyContent (R19): assistant messages with zero blocks are dropped;
// user messages with no content are sent as a single space.
func fixEmptyContent(msgs []Message) []Message {
	out := msgs[:0]
	for _, m := range msgs {
		if m.Role == RoleAssistant && len(m.Blocks) == 0 {
			continue
		}
		if m.Role == RoleUser {
			if len(m.Blocks) == 0 {
				m.Blocks = []Block{TextBlock(" ")}
			}
			for i, b := range m.Blocks {
				if b.Type == BlockText && strings.TrimSpace(b.Text) == "" {
					m.Blocks[i].Text = " "
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// downgradeDocuments (R38): a model that does not accept documents gets text
// in their place. The name goes with it — a model told a document was left
// out can ask for it another way, and one told nothing cannot.
func downgradeDocuments(msgs []Message) []Message {
	for i, m := range msgs {
		for j, b := range m.Blocks {
			if b.Type == BlockDocument {
				if b.Name != "" {
					msgs[i].Blocks[j] = TextBlock("[document " + b.Name + " omitted]")
					continue
				}
				msgs[i].Blocks[j] = TextBlock("[document omitted]")
			}
		}
	}
	return msgs
}

// downgradeImages (R20): non-vision models get image blocks replaced by text.
func downgradeImages(msgs []Message) []Message {
	for i, m := range msgs {
		for j, b := range m.Blocks {
			if b.Type == BlockImage {
				msgs[i].Blocks[j] = TextBlock("[image omitted]")
			}
		}
	}
	return msgs
}
