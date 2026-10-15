package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Terminal control sequences. The view runs in the alternate screen buffer so
// closing the popup leaves the pane's scrollback exactly as it was.
const (
	altEnter   = "\033[?1049h"
	altLeave   = "\033[?1049l"
	hideCursor = "\033[?25l"
	showCursor = "\033[?25h"
	cursorHome = "\033[H"
	clearBelow = "\033[0J"
	clearEOL   = "\033[K"
)

// isTTY reports whether f is a terminal (as opposed to a pipe or file).
func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// sttyRun shells out to stty, pointed at this process's own stdin.
//
// Using stdin rather than a --file flag is deliberate: BSD stty (macOS) spells
// that flag -f and GNU stty (Linux) spells it -F, and this plugin supports both.
// Operating on inherited stdin sidesteps the divergence entirely.
func sttyRun(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// termSize returns the terminal's (width, height) in cells.
//
// COLUMNS and LINES win when set, and are honored independently — either one
// alone still overrides that dimension — so the layout can be exercised at any
// size without a real terminal. Otherwise the size comes from `stty size`, and
// failing that from a conservative 80x24.
func termSize() (int, int) {
	width, height := 0, 0
	if out, err := sttyRun("size"); err == nil {
		// `stty size` prints "<rows> <cols>".
		if f := strings.Fields(out); len(f) == 2 {
			rows, errR := strconv.Atoi(f[0])
			cols, errC := strconv.Atoi(f[1])
			if errR == nil && errC == nil && rows > 0 && cols > 0 {
				width, height = cols, rows
			}
		}
	}
	if w := envInt("COLUMNS"); w > 0 {
		width = w
	}
	if h := envInt("LINES"); h > 0 {
		height = h
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func envInt(key string) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return 0
	}
	return n
}

// ttyState holds the terminal mode to restore on exit.
type ttyState struct{ saved string }

// enterCbreak puts the terminal in cbreak mode: keys arrive immediately and are
// not echoed, but output post-processing and Ctrl-C's SIGINT stay on. (Full raw
// mode would swallow both — newlines would no longer return the carriage, and
// Ctrl-C would have to be decoded by hand.)
func enterCbreak() (*ttyState, error) {
	saved, err := sttyRun("-g")
	if err != nil {
		return nil, err
	}
	if _, err := sttyRun("-icanon", "-echo", "min", "1", "time", "0"); err != nil {
		return nil, err
	}
	return &ttyState{saved: saved}, nil
}

// restore puts the terminal back exactly as stty -g captured it. Safe to call on
// a nil/zero state, so callers can defer it unconditionally.
func (t *ttyState) restore() {
	if t == nil || t.saved == "" {
		return
	}
	_, _ = sttyRun(t.saved)
}
