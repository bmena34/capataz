// The first-session prompt shown when `capataz` starts from a plain terminal
// and no tmux server exists. A minimal inline Bubble Tea program so the
// directory field gets the same Tab completion as the dashboard's new-session
// modal (completePath), instead of a bare stdin read.
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// errPromptAborted reports that the operator cancelled the first-session
// prompt (Esc/Ctrl+C); the caller exits quietly instead of creating anything.
var errPromptAborted = errors.New("prompt cancelled")

// promptDirAndName asks the operator for the directory and session name of the
// first session to create when no tmux server is running yet. A blank
// directory means home; a blank name defaults to the directory basename. If
// the interactive prompt can't run (no usable TTY), it falls back to plain
// stdin reads so a scripted/odd environment still works.
func promptDirAndName() (dir, name string, err error) {
	ti := textinput.New()
	ti.Prompt = "" // we render our own "› " prompt
	ti.Placeholder = "directory for the new session (blank = home)"
	ti.CharLimit = 2000
	ti.Focus()

	out, err := tea.NewProgram(bootstrapModel{input: ti}).Run()
	if err != nil {
		return promptDirAndNameStdin()
	}
	m, ok := out.(bootstrapModel)
	if !ok || m.abort {
		return "", "", errPromptAborted
	}
	return m.dir, m.name, nil
}

// bootstrapModel is the two-stage prompt: directory (with Tab completion),
// then session name. It mirrors the dashboard's modeNew modal keys — Tab
// completes, Enter advances, Esc steps back from the name to the directory
// and cancels from the directory.
type bootstrapModel struct {
	input textinput.Model
	stage int    // 0 = directory, 1 = name
	dir   string // captured (expanded) directory once stage 0 is done
	name  string // captured session name once stage 1 is done
	hint  string // completion feedback under the input
	done  bool
	abort bool
}

func (m bootstrapModel) Init() tea.Cmd { return textinput.Blink }

func (m bootstrapModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			m.abort = true
			return m, tea.Quit
		case "esc":
			if m.stage == 1 { // step back to the directory field
				m.stage = 0
				m.input.SetValue(m.dir)
				m.input.CursorEnd()
				m.input.Placeholder = "directory for the new session (blank = home)"
				m.hint = ""
				return m, nil
			}
			m.abort = true
			return m, tea.Quit
		case "tab":
			if m.stage != 0 { // completion only applies to the directory field
				return m, nil
			}
			completed, matches := completePath(m.input.Value())
			m.input.SetValue(completed)
			m.input.CursorEnd()
			switch {
			case len(matches) == 0:
				m.hint = "no matching directories"
			case len(matches) > 1:
				m.hint = "matches: " + strings.Join(firstN(matches, 8), "  ")
			default:
				m.hint = ""
			}
			return m, nil
		case "enter":
			if m.stage == 0 { // directory captured → advance to naming
				dir := strings.TrimSpace(m.input.Value())
				if dir == "" {
					dir, _ = os.UserHomeDir()
				}
				m.dir = expandHome(dir)
				m.stage = 1
				m.input.SetValue(sessionBase(m.dir)) // sensible default name
				m.input.CursorEnd()
				m.input.Placeholder = "session name"
				m.hint = ""
				return m, nil
			}
			m.name = strings.TrimSpace(m.input.Value())
			if m.name == "" {
				m.name = sessionBase(m.dir)
			}
			m.done = true
			return m, tea.Quit
		}
		m.hint = ""
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m bootstrapModel) View() string {
	if m.done || m.abort {
		return ""
	}
	title, help := "Directory for the new session (blank = home)", "Tab complete · ↵ next · Esc cancel"
	if m.stage == 1 {
		title, help = "Session name", "↵ create · Esc back"
	}
	var b strings.Builder
	b.WriteString("No tmux session found — let's start one.\n\n")
	b.WriteString(title + "\n")
	b.WriteString("› " + m.input.View() + "\n")
	if m.hint != "" {
		b.WriteString("  " + m.hint + "\n")
	}
	b.WriteString("  " + help + "\n")
	return b.String()
}

// promptDirAndNameStdin is the pre-Bubble-Tea fallback: plain line reads, no
// completion. Only used when the interactive prompt can't take over the TTY.
func promptDirAndNameStdin() (dir, name string, err error) {
	r := bufio.NewReader(os.Stdin)
	fmt.Println("No tmux session found — let's start one.")
	fmt.Print("Directory for the new session (blank = home): ")
	line, _ := r.ReadString('\n')
	dir = strings.TrimSpace(line)
	if dir == "" {
		dir, _ = os.UserHomeDir()
	} else {
		dir = expandHome(dir)
	}
	fmt.Printf("Session name (blank = %s): ", sessionBase(dir))
	line, _ = r.ReadString('\n')
	name = strings.TrimSpace(line)
	if name == "" {
		name = sessionBase(dir)
	}
	return dir, name, nil
}
