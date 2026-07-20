package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func newBootstrapModel() bootstrapModel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	return bootstrapModel{input: ti}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// Blank directory + accepted default name: dir falls back to home, name to its
// basename, and the model finishes.
func TestBootstrapPromptDefaults(t *testing.T) {
	m := newBootstrapModel()

	next, _ := m.Update(key("enter"))
	m = next.(bootstrapModel)
	home, _ := os.UserHomeDir()
	if m.dir != home {
		t.Fatalf("blank directory: got %q, want home %q", m.dir, home)
	}
	if m.stage != 1 {
		t.Fatalf("stage after directory enter = %d, want 1", m.stage)
	}
	if got, want := m.input.Value(), sessionBase(home); got != want {
		t.Fatalf("default name %q, want %q", got, want)
	}

	next, _ = m.Update(key("enter"))
	m = next.(bootstrapModel)
	if !m.done || m.abort {
		t.Fatalf("done=%v abort=%v after name enter, want done", m.done, m.abort)
	}
	if m.name != sessionBase(home) {
		t.Fatalf("name %q, want %q", m.name, sessionBase(home))
	}
}

// Tab on the directory field completes against the real filesystem.
func TestBootstrapPromptTabCompletes(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "myproject"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := newBootstrapModel()
	m.input.SetValue(filepath.Join(base, "mypro"))
	next, _ := m.Update(key("tab"))
	m = next.(bootstrapModel)
	want := filepath.Join(base, "myproject") + "/"
	if m.input.Value() != want {
		t.Fatalf("tab completed to %q, want %q", m.input.Value(), want)
	}
}

// Esc steps back from the name field to the directory field (preserving the
// captured directory), and cancels from the directory field.
func TestBootstrapPromptEsc(t *testing.T) {
	dir := t.TempDir()

	m := newBootstrapModel()
	m.input.SetValue(dir)
	next, _ := m.Update(key("enter"))
	m = next.(bootstrapModel)
	if m.stage != 1 {
		t.Fatalf("stage = %d, want 1", m.stage)
	}

	next, _ = m.Update(key("esc"))
	m = next.(bootstrapModel)
	if m.stage != 0 || m.abort {
		t.Fatalf("esc from name: stage=%d abort=%v, want back at 0", m.stage, m.abort)
	}
	if m.input.Value() != dir {
		t.Fatalf("directory field restored to %q, want %q", m.input.Value(), dir)
	}

	next, _ = m.Update(key("esc"))
	m = next.(bootstrapModel)
	if !m.abort {
		t.Fatal("esc from directory field should abort")
	}
}

// The view never contains the internal placeholder text after completion hints
// fire, and always shows the current stage's help line.
func TestBootstrapPromptView(t *testing.T) {
	m := newBootstrapModel()
	v := m.View()
	if !strings.Contains(v, "Tab complete") {
		t.Fatalf("directory-stage view missing completion help: %q", v)
	}
	next, _ := m.Update(key("enter"))
	m = next.(bootstrapModel)
	if v := m.View(); !strings.Contains(v, "Session name") {
		t.Fatalf("name-stage view missing title: %q", v)
	}
}
