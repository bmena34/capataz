package tui

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// maxConvoMessages caps how many recent turns the conversation view renders.
const maxConvoMessages = 40

var (
	youStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	claudeStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
)

// renderTranscript reads a Claude Code transcript (JSONL) and renders the most
// recent turns as a readable, width-wrapped conversation.
func renderTranscript(path string, width int) string {
	if path == "" {
		return "This session has no transcript yet."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "Could not read transcript: " + err.Error()
	}
	if width < 20 {
		width = 20
	}
	wrap := lipgloss.NewStyle().Width(width)

	var msgs []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		role, _ := rec["type"].(string)
		switch role {
		case "user":
			if text := userText(rec); text != "" {
				msgs = append(msgs, youStyle.Render("You")+"\n"+wrap.Render(text))
			}
		case "assistant":
			if block := assistantBlock(rec, wrap); block != "" {
				msgs = append(msgs, claudeStyle.Render("Claude")+"\n"+block)
			}
		}
	}

	if len(msgs) == 0 {
		return "No conversation yet."
	}
	if len(msgs) > maxConvoMessages {
		msgs = msgs[len(msgs)-maxConvoMessages:]
	}
	return strings.Join(msgs, "\n\n")
}

// userText extracts a real user prompt, skipping tool-result turns (which the
// transcript also records as "user").
func userText(rec map[string]any) string {
	msg, ok := rec["message"].(map[string]any)
	if !ok {
		return ""
	}
	switch c := msg["content"].(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		var b strings.Builder
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if s, ok := block["text"].(string); ok {
					b.WriteString(s)
				}
			}
			// tool_result blocks are intentionally skipped.
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// assistantBlock renders assistant text plus a compact note for any tool use.
func assistantBlock(rec map[string]any, wrap lipgloss.Style) string {
	msg, ok := rec["message"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := msg["content"].([]any)
	if !ok {
		if s, ok := msg["content"].(string); ok {
			return wrap.Render(strings.TrimSpace(s))
		}
		return ""
	}
	var parts []string
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch block["type"] {
		case "text":
			if s, ok := block["text"].(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, wrap.Render(strings.TrimSpace(s)))
			}
		case "tool_use":
			name, _ := block["name"].(string)
			parts = append(parts, toolStyle.Render("⚙ "+name))
		}
	}
	return strings.Join(parts, "\n")
}
