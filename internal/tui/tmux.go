package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/logging"
)

// conf returns the user config, loaded once and cached.
var (
	cfgOnce sync.Once
	cfgVal  config.Config
)

func conf() config.Config {
	cfgOnce.Do(func() { cfgVal = config.Load() })
	return cfgVal
}

// clientSize returns an attached tmux client's width and height in cells, or
// 0,0 if it can't be determined. Uses list-clients (not display-message -p),
// which resolves reliably even when called from inside a popup where there's no
// current-target client.
func clientSize() (w, h int) {
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_width}\t#{client_height}").Output()
	if err != nil {
		return 0, 0
	}
	for _, ln := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(ln), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		w, _ = strconv.Atoi(parts[0])
		h, _ = strconv.Atoi(parts[1])
		if w > 0 && h > 0 {
			return w, h
		}
	}
	return 0, 0
}

// jump focuses the given tmux pane: switches the attached client to its
// session, selects its window, and selects the pane.
func jump(pane string) error {
	logging.Infof("jump pane=%q", pane)
	if pane == "" {
		return fmt.Errorf("session has no tmux pane")
	}
	target, err := exec.Command("tmux", "display-message", "-p", "-t", pane,
		"#{session_name}\t#{session_name}:#{window_index}").Output()
	if err != nil {
		logging.Errorf("jump pane=%q not found: %v", pane, err)
		return fmt.Errorf("pane %s not found (session may have ended)", pane)
	}
	fields := strings.SplitN(strings.TrimSpace(string(target)), "\t", 2)
	if len(fields) != 2 {
		return fmt.Errorf("unexpected tmux output")
	}
	session, window := fields[0], fields[1]
	_ = exec.Command("tmux", "switch-client", "-t", session).Run()
	_ = exec.Command("tmux", "select-window", "-t", window).Run()
	if out, err := exec.Command("tmux", "select-pane", "-t", pane).CombinedOutput(); err != nil {
		return fmt.Errorf("select-pane: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// reply sends literal text followed by Enter into the given pane.
func reply(pane, text string) error {
	logging.Infof("reply pane=%q len=%d", pane, len(text))
	if pane == "" {
		logging.Errorf("reply: session has no tmux pane")
		return fmt.Errorf("session has no tmux pane")
	}
	// -l sends keys literally; "--" guards text that starts with a dash.
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, "-l", "--", text).CombinedOutput(); err != nil {
		logging.Errorf("reply send-keys pane=%q: %s", pane, strings.TrimSpace(string(out)))
		return fmt.Errorf("send-keys: %s", strings.TrimSpace(string(out)))
	}
	// Claude Code's input widget needs a beat to commit the injected text before
	// the Enter submits it; without this the Enter can land on an empty/partial
	// buffer and nothing happens.
	time.Sleep(150 * time.Millisecond)
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, "Enter").CombinedOutput(); err != nil {
		logging.Errorf("reply Enter pane=%q: %s", pane, strings.TrimSpace(string(out)))
		return fmt.Errorf("send-keys Enter: %s", strings.TrimSpace(string(out)))
	}
	logging.Infof("reply sent pane=%q", pane)
	return nil
}

// gracefulExit asks Claude to quit cleanly: it interrupts any in-progress work,
// clears the prompt, then sends the /quit command. When Claude exits its pane
// closes and its SessionEnd hook removes it from the dashboard. Tearing down the
// window (killWindow, via killFallback) is the backstop + sidebar cleanup.
func gracefulExit(pane string) error {
	if pane == "" {
		logging.Errorf("gracefulExit: session has no tmux pane")
		return fmt.Errorf("session has no tmux pane")
	}
	logging.Infof("gracefulExit pane=%q", pane)
	// Escape interrupts any running generation so the input accepts a command.
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, "Escape").CombinedOutput(); err != nil {
		return fmt.Errorf("send Escape: %s", strings.TrimSpace(string(out)))
	}
	time.Sleep(150 * time.Millisecond)
	// Clear anything half-typed in the prompt, then issue /quit.
	_ = exec.Command("tmux", "send-keys", "-t", pane, "C-u").Run()
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, "-l", "--", "/quit").CombinedOutput(); err != nil {
		return fmt.Errorf("send /quit: %s", strings.TrimSpace(string(out)))
	}
	time.Sleep(150 * time.Millisecond)
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("send Enter: %s", strings.TrimSpace(string(out)))
	}
	logging.Infof("gracefulExit sent /quit pane=%q", pane)
	return nil
}

// currentWindow returns the window id the attached client is currently showing.
func currentWindow() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// killWindow closes a whole tmux window and every pane in it — used to tear down
// an agent's window (its claude pane and the capataz sidebar) on kill.
func killWindow(window string) error {
	if window == "" {
		return fmt.Errorf("no window")
	}
	if out, err := exec.Command("tmux", "kill-window", "-t", window).CombinedOutput(); err != nil {
		return fmt.Errorf("kill-window: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// inTmux reports whether capataz is running inside a tmux pane.
func inTmux() bool { return os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != "" }

// paneOrigin returns the tmux session name and window id containing a pane.
func paneOrigin(pane string) (session, windowID string, err error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane,
		"#{session_name}\t#{window_id}").Output()
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected tmux output %q", string(out))
	}
	return parts[0], parts[1], nil
}


// newClaudeSession creates a new detached tmux session running claude in dir
// and returns the new pane's id. name is the session name (sanitized and
// deduplicated; falls back to the dir basename when blank), so each agent gets
// its own named session whose layout travels with it.
func newClaudeSession(name, dir string) (string, error) {
	name = sanitizeName(name)
	if name == "" {
		name = sessionBase(dir)
	}
	name = uniqueSessionName(name)
	args := []string{"new-session", "-d", "-s", name, "-P", "-F", "#{pane_id}", "-n", "claude"}
	// Create the session at the client's real size so attachSidebar's sidebar
	// split is N cols of the full width — not N of the detached default (~80),
	// which would scale up to a ~50/50 split once you switch to it.
	if w, h := clientSize(); w > 0 && h > 0 {
		args = append(args, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, "claude")
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("new-session: %v", err)
	}
	pane := strings.TrimSpace(string(out))
	logging.Infof("new session %q pane=%s dir=%s", name, pane, dir)
	return pane, nil
}

// attachSidebar splits a narrow capataz dashboard pane to the left of
// targetPane. The dashboard runs as a normal (non-popup) pane, so selecting a
// session switches without closing the sidebar. The -d flag keeps focus on the
// work pane so the sidebar never steals it.
func attachSidebar(targetPane string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	width := strconv.Itoa(conf().Sidebar.Width)
	args := []string{"split-window", "-h", "-b", "-d", "-l", width, "-t", targetPane, self}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("split sidebar: %s", strings.TrimSpace(string(out)))
	}
	// Keep focus on the work pane and force a redraw so it doesn't look stuck
	// until the next keypress.
	_ = exec.Command("tmux", "select-pane", "-t", targetPane).Run()
	_ = exec.Command("tmux", "refresh-client", "-S").Run()
	return nil
}

// newSessionWithSidebar creates a new claude session and attaches a dashboard
// sidebar to it. A sidebar failure is non-fatal — the session is still usable.
func newSessionWithSidebar(name, dir string) (string, error) {
	pane, err := newClaudeSession(name, dir)
	if err != nil {
		return "", err
	}
	if err := attachSidebar(pane); err != nil {
		logging.Errorf("attach sidebar to new session: %v", err)
	}
	return pane, nil
}

// Attach adds a dashboard sidebar to the window containing pane (defaulting to
// the caller's own $TMUX_PANE), unless one is already present. Invoked as
// `capataz attach [pane]` — the keybinding passes the active pane explicitly so
// it works even when run in the background by tmux.
func Attach(pane string) error {
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}
	if pane == "" {
		return fmt.Errorf("run `capataz attach` inside a tmux session")
	}
	// Skip if that window already has a capataz pane — ignoring the caller's own
	// pane, which may transiently be running `capataz attach`.
	out, _ := exec.Command("tmux", "list-panes", "-t", pane, "-F", "#{pane_id}\t#{pane_current_command}").Output()
	for _, ln := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(ln), "\t", 2)
		if len(parts) == 2 && parts[0] != pane && parts[1] == "capataz" {
			fmt.Println("this window already has a capataz sidebar")
			return nil
		}
	}
	if err := attachSidebar(pane); err != nil {
		return err
	}
	fmt.Println("attached capataz sidebar")
	return nil
}

// InstallNavKeybind installs vim-tmux-navigator-style Ctrl+h/j/k/l: when the
// focused pane is running (n)vim the key is passed through to it, otherwise it
// moves tmux focus. This lets the same keys move between nvim splits and tmux
// panes — including the capataz sidebar. Live + prints the persistent config.
func InstallNavKeybind() error {
	if !inTmux() {
		return fmt.Errorf("run inside tmux to bind the nav keys")
	}
	isVim := `ps -o state= -o comm= -t '#{pane_tty}' | grep -iqE '^[^TXZ ]+ +(\S+\/)?g?(view|l?n?vim?x?|fzf)(diff)?(-wrapped)?$'`
	dirs := []struct{ key, dir string }{
		{"C-h", "-L"}, {"C-j", "-D"}, {"C-k", "-U"}, {"C-l", "-R"},
	}
	for _, d := range dirs {
		args := []string{"bind-key", "-n", d.key, "if-shell", isVim, "send-keys " + d.key, "select-pane " + d.dir}
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("bind %s: %s", d.key, strings.TrimSpace(string(out)))
		}
	}
	logging.Infof("bound vim-style nav keys C-h/j/k/l")
	fmt.Println("Bound Ctrl+h/j/k/l to move between panes (passes through to nvim) — live in this server.")
	fmt.Println("\nTo persist, add this to ~/.tmux.conf:")
	fmt.Printf("  is_vim=\"%s\"\n", isVim)
	for _, d := range dirs {
		fmt.Printf("  bind-key -n %s if-shell \"$is_vim\" 'send-keys %s' 'select-pane %s'\n", d.key, d.key, d.dir)
	}
	fmt.Println("\n(Note: in non-vim panes Ctrl+l now moves focus instead of clearing the screen.)")
	return nil
}

// InstallAttachKeybind binds prefix+key to drop a dashboard sidebar into the
// current window — live in the running server — and prints the line to add to
// ~/.tmux.conf for persistence. The binding passes the active pane id so attach
// targets the right window.
func InstallAttachKeybind(key string) error {
	if key == "" {
		key = "a"
	}
	if !inTmux() {
		return fmt.Errorf("run inside tmux to bind the attach key")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// Redirect output to /dev/null: tmux run-shell otherwise pops the command's
	// stdout into a view-mode buffer you'd have to dismiss with q.
	shellCmd := fmt.Sprintf("'%s' attach #{pane_id} >/dev/null 2>&1", self)
	if out, err := exec.Command("tmux", "bind-key", key, "run-shell", "-b", shellCmd).CombinedOutput(); err != nil {
		return fmt.Errorf("bind-key: %s", strings.TrimSpace(string(out)))
	}
	logging.Infof("bound prefix+%s to capataz attach", key)
	fmt.Printf("Bound prefix+%s to attach a capataz sidebar (live in this server).\n\n", key)
	fmt.Println("To make it permanent, add this to ~/.tmux.conf:")
	fmt.Printf("  bind-key %s run-shell -b \"%s\"\n", key, shellCmd)
	return nil
}

// renameSession renames the tmux session that contains pane.
func renameSession(pane, newName string) error {
	newName = sanitizeName(newName)
	if newName == "" {
		return fmt.Errorf("name is empty")
	}
	if pane == "" {
		return fmt.Errorf("session has no tmux pane")
	}
	session, _, err := paneOrigin(pane)
	if err != nil {
		return err
	}
	if out, err := exec.Command("tmux", "rename-session", "-t", session, newName).CombinedOutput(); err != nil {
		return fmt.Errorf("rename-session: %s", strings.TrimSpace(string(out)))
	}
	logging.Infof("renamed session %q -> %q (pane=%s)", session, newName, pane)
	return nil
}

// sanitizeName makes a string safe as a tmux session name: tmux treats '.' and
// ':' specially, and spaces are awkward, so they become underscores.
func sanitizeName(s string) string {
	return strings.NewReplacer(".", "_", ":", "_", " ", "_").Replace(strings.TrimSpace(s))
}

// sessionBase derives a default tmux-safe session name from a directory path.
func sessionBase(dir string) string {
	if dir == "" {
		return "claude"
	}
	b := sanitizeName(filepath.Base(strings.TrimRight(dir, "/")))
	if b == "" || b == "/" || b == "_" {
		return "claude"
	}
	return b
}

// livePaneSessions maps each existing pane id to its tmux session name, or nil
// if tmux can't be queried. Used to label sessions in the dashboard.
func livePaneSessions() map[string]string {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{session_name}").Output()
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, ln := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(ln), "\t", 2)
		if len(parts) == 2 && parts[0] != "" {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

// uniqueSessionName appends -2, -3, … until the name is free.
func uniqueSessionName(base string) string {
	name := base
	for i := 2; hasSession(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// hasSession reports whether a tmux session with exactly this name exists.
func hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// popupArgs builds the `tmux display-popup` invocation that runs the dashboard
// as a transient overlay. CAPATAZ_POPUP tells the TUI it's in popup mode (no
// pane staging / global key bindings); -E closes the popup when capataz exits.
func popupArgs(self string) []string {
	return []string{"display-popup", "-E", "-w", "90%", "-h", "85%", "-e", "CAPATAZ_POPUP=1", "-T", " capataz ", self}
}

// LaunchPopup opens the dashboard in a tmux popup over the current client.
func LaunchPopup() error {
	if !inTmux() {
		return fmt.Errorf("run inside tmux: `capataz popup` opens a tmux popup")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return exec.Command("tmux", popupArgs(self)...).Run()
}

// InstallKeybind binds prefix+key to summon the dashboard popup — live in the
// running tmux server — and prints the line to add to ~/.tmux.conf so it
// survives a server restart.
func InstallKeybind(key string) error {
	if key == "" {
		key = "g"
	}
	if !inTmux() {
		return fmt.Errorf("run inside tmux to bind the popup key")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{"bind-key", key}, popupArgs(self)...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("bind-key: %s", strings.TrimSpace(string(out)))
	}
	logging.Infof("bound prefix+%s to capataz popup", key)
	fmt.Printf("Bound prefix+%s to the capataz dashboard popup (live in this server).\n\n", key)
	fmt.Println("To make it permanent, add this to ~/.tmux.conf:")
	fmt.Printf("  bind-key %s display-popup -E -w 90%% -h 85%% -e CAPATAZ_POPUP=1 -T ' capataz ' '%s'\n", key, self)
	return nil
}

// tmuxOption returns the value of a global tmux option (empty if unset).
func tmuxOption(name string) string {
	out, err := exec.Command("tmux", "show-options", "-gv", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// EnableStatusline prepends the capataz needs-you badge (#{@capataz_needs}) to
// the tmux status-right, live in the running server, and prints the line to add
// to ~/.tmux.conf for persistence. Idempotent; non-destructive (it keeps the
// existing status-right).
func EnableStatusline() error {
	if !inTmux() {
		return fmt.Errorf("run inside tmux to wire the status line")
	}
	cur := tmuxOption("status-right")
	if strings.Contains(cur, "@capataz_needs") {
		fmt.Println("status-right already shows the capataz badge.")
		return nil
	}
	newVal := "#{@capataz_needs}"
	if cur != "" {
		newVal = "#{@capataz_needs} " + cur
	}
	if out, err := exec.Command("tmux", "set-option", "-g", "status-right", newVal).CombinedOutput(); err != nil {
		return fmt.Errorf("set status-right: %s", strings.TrimSpace(string(out)))
	}
	logging.Infof("enabled capataz status badge")
	fmt.Println("Added the capataz needs-you badge to status-right (live in this server).")
	fmt.Println("\nTo persist, set this in ~/.tmux.conf:")
	fmt.Printf("  set -g status-right '%s'\n", newVal)
	fmt.Println("\nDisable with: capataz statusline off")
	return nil
}

// DisableStatusline removes the badge token from status-right (live) and clears
// the user option.
func DisableStatusline() error {
	if !inTmux() {
		return fmt.Errorf("run inside tmux")
	}
	cur := tmuxOption("status-right")
	newVal := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(cur, "#{@capataz_needs} ", ""), "#{@capataz_needs}", ""))
	_ = exec.Command("tmux", "set-option", "-g", "status-right", newVal).Run()
	_ = exec.Command("tmux", "set-option", "-gu", "@capataz_needs").Run()
	_ = exec.Command("tmux", "refresh-client", "-S").Run()
	fmt.Println("Removed the capataz badge from status-right (live). Also remove it from ~/.tmux.conf if you persisted it.")
	return nil
}
