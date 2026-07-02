// Package tui renders the capataz dashboard: a live, needs-you-first table of
// Claude Code sessions with jump / reply / peek / log actions.
package tui

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/logging"
	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/proto"
	"github.com/bmena34/capataz/internal/store"
)

type mode int

const (
	modeNormal mode = iota
	modeReply
	modePeek
	modeLog
	modeConvo
	modeNew
	modeHelp
	modeKill
	modeRename
	modeIssues
	modeStartWork
)

type tickMsg time.Time

type model struct {
	sessions   []proto.Session
	cursor     int
	selectedID string
	mode       mode
	input      textinput.Model
	vp         viewport.Model
	width      int
	height     int
	connected  bool
	status     string
	msgCh      chan tea.Msg

	// convoID is the session whose conversation is open (empty if none).
	convoID string
	// killID is the session awaiting kill confirmation (empty if none).
	killID string
	// gPending is true after a lone `g`, awaiting a second `g` (vim `gg`).
	gPending bool
	// newHint shows directory-completion matches in the New session popup.
	newHint string
	// newStage is the New-session step: 0 = directory, 1 = name. newDir holds the
	// directory captured in step 0.
	newStage int
	newDir   string
	// paneSession maps pane id -> tmux session name, refreshed each tick so the
	// dashboard can label sessions by their (renameable) tmux name.
	paneSession map[string]string
	// limits is the account's live usage (session + weekly), pushed by the daemon.
	limits proto.Usage
	// popup is true when launched as a tmux display-popup (transient switcher):
	// selecting a session switches to it and closes the popup. When false the
	// dashboard is a persistent sidebar pane and selecting never quits it.
	popup bool
	// lastWinWidth/desiredWidth keep the sidebar a fixed width across window
	// resizes (tmux otherwise scales it proportionally). desiredWidth starts at
	// the config width and updates when the user manually resizes the pane.
	lastWinWidth int
	desiredWidth int
	// attachedSession is the tmux session name the user's client is currently
	// viewing, so the dashboard can mark which Claude session they're attached to.
	attachedSession string

	// Issue view (modeIssues): the backlog of the selected session's repo,
	// flattened across features in feature+number order, and a cursor into it.
	// issueCwd is the selected session's raw cwd, cached to detect when the
	// selection moves to a different directory. issueRepo is that cwd resolved to
	// its primary git checkout (where the .scratch backlog lives — so a session
	// running in a linked worktree still shows the shared backlog); issueRepoName
	// is its display name. The primary is resolved (a git subprocess) only when
	// issueCwd changes, never per tick or on the render path.
	issueList     []issues.Issue
	issueCursor   int
	issueCwd      string
	issueRepo     string
	issueRepoName string
	// issueSelected is the multi-select set for parallel dispatch, keyed by issue
	// Path (stable across reloads). Cleared after a dispatch and when the selection
	// moves to a different repo (its issues are no longer in view).
	issueSelected map[string]bool
	// startIssue backs the start-work chooser (modeStartWork): the issue picked.
	// The hand-off target pane is NOT cached here — it's resolved live from
	// m.current() when h is pressed, so a pane the daemon's sweep pruned while
	// the chooser was open can't be replied to (see handleStartWorkKey).
	startIssue issues.Issue
	// readyMerges caches the dispatched worktree branches whose issue is resolved
	// and so are ready to merge back (Round 2). Like issueList it's refreshed off
	// the render path (syncSelectedIssues shells out to git worktree list), so the
	// issues view and the `m` merge action read it without a git call per render.
	readyMerges []mergeCandidate
}

// Run starts the dashboard. It blocks until the user quits.
func Run() error {
	ti := textinput.New()
	ti.Placeholder = "type your reply, Enter to send, Esc to cancel"
	ti.Prompt = "" // we render our own "› " prompt
	ti.CharLimit = 2000

	m := model{
		input:  ti,
		vp:     viewport.New(0, 0),
		status: "connecting…",
		msgCh:  make(chan tea.Msg, 256),
	}
	m.popup = os.Getenv("CAPATAZ_POPUP") == "1"
	m.paneSession = livePaneSessions()
	m.attachedSession = currentAttachedSession()
	writePID()

	p := tea.NewProgram(m, tea.WithAltScreen())

	// SIGTERM (from `capataz quit`) triggers a graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Quit()
	}()

	_, err := p.Run()
	signal.Stop(sigCh)
	removePID()
	return err
}

// Quit signals every running capataz dashboard to shut down gracefully, and
// clears any stale PID markers. (Multiple dashboards can run at once — one per
// session sidebar plus any popup.)
func Quit() error {
	entries, err := os.ReadDir(paths.PidDir())
	if err != nil || len(entries) == 0 {
		fmt.Println("no capataz dashboards running")
		return nil
	}
	signaled := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		marker := filepath.Join(paths.PidDir(), e.Name())
		proc, _ := os.FindProcess(pid) // always non-nil on Unix
		if proc.Signal(syscall.SIGTERM) != nil {
			_ = os.Remove(marker) // process gone — clear the stale marker
			continue
		}
		signaled++
	}
	if signaled == 0 {
		fmt.Println("no running capataz dashboards (cleared stale markers)")
	} else {
		fmt.Printf("quit signal sent to %d dashboard(s)\n", signaled)
	}
	return nil
}

// writePID/removePID register this dashboard in the PID directory (one marker
// file per instance, named by PID) so `capataz quit` can find them all.
func writePID() {
	_ = os.MkdirAll(paths.PidDir(), 0o755)
	_ = os.WriteFile(filepath.Join(paths.PidDir(), strconv.Itoa(os.Getpid())), nil, 0o644)
}
func removePID() { _ = os.Remove(filepath.Join(paths.PidDir(), strconv.Itoa(os.Getpid()))) }

func (m model) Init() tea.Cmd {
	go listen(m.msgCh)
	return tea.Batch(waitForMsg(m.msgCh), tick())
}

// pinSidebar keeps a docked sidebar at a fixed width. On a *window* resize it
// restores the pane to its desired width (so shrinking the terminal squeezes
// the work pane, not the sidebar); when the user manually resizes the pane (the
// window width is unchanged) it remembers that new width instead. No-op in popup
// mode or when the dashboard is the only pane in its window.
func (m *model) pinSidebar() {
	if m.popup {
		return
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	ww, panes := windowWidthAndPanes(pane)
	if ww == 0 || panes < 2 {
		m.lastWinWidth = ww // not docked — nothing to pin against
		return
	}
	if m.desiredWidth == 0 {
		m.desiredWidth = conf().Sidebar.Width
	}
	if ww != m.lastWinWidth { // window resized → restore the fixed width
		m.lastWinWidth = ww
		pinPaneWidth(pane, m.desiredWidth)
	} else { // same window, our pane changed → user resized it; remember
		m.desiredWidth = m.width
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutModal()
		m.pinSidebar()
		return m, nil

	case tickMsg:
		if pm := livePaneSessions(); pm != nil {
			m.paneSession = pm
		}
		m.attachedSession = currentAttachedSession()
		if m.mode == modeConvo {
			m.refreshConvo(true)
		}
		// Refresh the selected repo's backlog so both the inline panel and the
		// modal pick up claim/resolve edits agents make on disk.
		m.syncSelectedIssues(true)
		return m, tick()

	case killFallbackMsg:
		// Tear down the whole window: closes the (now-exited) claude pane's window
		// and the capataz sidebar in it. Also force-kills claude if /quit didn't
		// take. Best-effort — the window may already be gone if there was no sidebar.
		if msg.window != "" {
			// If the client is currently on the window we're about to kill, hop it
			// to another managed session first so we don't get dropped out of tmux.
			if currentWindow() == msg.window {
				if other, ok := m.anotherSession(msg.id); ok {
					_ = jump(other.TmuxPane)
				}
			}
			if err := killWindow(msg.window); err != nil {
				logging.Infof("kill window %s: %v", msg.window, err)
			} else {
				logging.Infof("killed window %s", msg.window)
			}
		}
		m.remove(msg.id)
		m.resort()
		return m, nil

	case connMsg:
		if msg.connected != m.connected {
			logging.Infof("daemon connection: connected=%v %s", msg.connected, msg.text)
		}
		m.connected = msg.connected
		if msg.text != "" {
			m.status = msg.text
		} else if msg.connected {
			m.status = "connected"
		}
		return m, waitForMsg(m.msgCh)

	case pushMsg:
		m.applyPush(msg.push)
		m.syncSelectedIssues(false) // selection may have changed repos
		return m, waitForMsg(m.msgCh)

	case tea.KeyMsg:
		next, cmd := m.handleKey(msg)
		// Keep the cached backlog pointed at the selected session after any key
		// that moved the cursor to a different repo.
		if nm, ok := next.(model); ok {
			nm.syncSelectedIssues(false)
			return nm, cmd
		}
		return next, cmd
	}
	return m, nil
}

// applyPush updates state from a daemon push. capataz only tracks tmux sessions,
// so every session here is actionable.
func (m *model) applyPush(p proto.Push) {
	if p.Usage != nil { // usage rides along on snapshots and its own pushes
		m.limits = *p.Usage
	}
	switch p.Type {
	case "usage":
		return // usage already applied above
	case "snapshot":
		m.sessions = m.sessions[:0]
		for _, s := range p.Sessions {
			if s.TmuxPane != "" { // defensive: ignore any detached strays
				m.sessions = append(m.sessions, s)
			}
		}
	case "update":
		if p.Session != nil && p.Session.TmuxPane != "" {
			m.upsert(*p.Session)
		}
	case "remove":
		if p.Session != nil {
			m.remove(p.Session.ID)
		}
	}
	m.resort()
}

func (m *model) upsert(s proto.Session) {
	for i := range m.sessions {
		if m.sessions[i].ID == s.ID {
			m.sessions[i] = s
			return
		}
	}
	m.sessions = append(m.sessions, s)
}

func (m *model) remove(id string) {
	out := m.sessions[:0]
	for _, s := range m.sessions {
		if s.ID != id {
			out = append(out, s)
		}
	}
	m.sessions = out
}

func (m *model) resort() {
	sort.Slice(m.sessions, func(i, j int) bool {
		pi, pj := m.sessions[i].State.Priority(), m.sessions[j].State.Priority()
		if pi != pj {
			return pi < pj
		}
		return m.sessions[i].LastChange > m.sessions[j].LastChange
	})
	// Keep the cursor on the same session across re-sorts when possible.
	if m.selectedID != "" {
		for i, s := range m.sessions {
			if s.ID == m.selectedID {
				m.cursor = i
				return
			}
		}
	}
	m.clampCursor()
}

func (m *model) clampCursor() {
	if m.cursor >= len(m.sessions) {
		m.cursor = len(m.sessions) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		m.selectedID = m.sessions[m.cursor].ID
	} else {
		m.selectedID = ""
	}
}

// label is the dashboard's name for a session: its (renameable) tmux session
// name when known, falling back to the working-directory basename.
func (m model) label(s proto.Session) string {
	if name := m.paneSession[s.TmuxPane]; name != "" {
		return name
	}
	return short(s.Cwd)
}

func (m *model) current() (proto.Session, bool) {
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		return m.sessions[m.cursor], true
	}
	return proto.Session{}, false
}

// anotherSession returns some other managed (paned) session, preferring the
// needs-you-first order. Used to hop the client somewhere when killing the
// session it's currently attached to, instead of dropping out of tmux.
func (m *model) anotherSession(excludeID string) (proto.Session, bool) {
	for _, s := range m.sessions {
		if s.ID != excludeID && s.TmuxPane != "" {
			return s, true
		}
	}
	return proto.Session{}, false
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeHelp {
		m.mode = modeNormal // any key closes the help
		return m, nil
	}
	if m.mode == modeReply {
		return m.handleReplyKey(msg)
	}
	if m.mode == modeNew {
		return m.handleNewKey(msg)
	}
	if m.mode == modeConvo {
		return m.handleConvoKey(msg)
	}
	if m.mode == modeKill {
		return m.handleKillKey(msg)
	}
	if m.mode == modeRename {
		return m.handleRenameKey(msg)
	}
	if m.mode == modeIssues {
		return m.handleIssuesKey(msg)
	}
	if m.mode == modeStartWork {
		return m.handleStartWorkKey(msg)
	}
	if m.mode == modeLog {
		key := msg.String()
		if m.gPending {
			m.gPending = false
			if key == "g" {
				m.vp.GotoTop()
				return m, nil
			}
		}
		switch key {
		case "esc", "q":
			m.mode = modeNormal
			return m, nil
		case "g":
			m.gPending = true
			return m, nil
		case "G":
			m.vp.GotoBottom()
			return m, nil
		}
		// viewport's default keymap already handles j/k, ctrl+d/u, f/b, arrows.
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	// Normal mode.
	key := msg.String()
	if m.gPending {
		m.gPending = false
		if key == "g" { // gg -> top
			m.cursor = 0
			m.clampCursor()
			return m, nil
		}
		// otherwise fall through and handle this key normally
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit // cleanup() runs after the program loop exits
	case "esc":
		if m.popup {
			return m, tea.Quit // esc dismisses the transient popup like a menu
		}
	case "?":
		m.mode = modeHelp
	case "up", "k":
		m.cursor--
		m.clampCursor()
	case "down", "j":
		m.cursor++
		m.clampCursor()
	case "ctrl+u":
		m.cursor -= 5
		m.clampCursor()
	case "ctrl+d":
		m.cursor += 5
		m.clampCursor()
	case "g": // start of gg
		m.gPending = true
	case "G": // bottom
		m.cursor = len(m.sessions) - 1
		m.clampCursor()
	case "enter", "o": // switch the client to the session
		if s, ok := m.current(); ok {
			if err := jump(s.TmuxPane); err != nil {
				m.status = "open failed: " + err.Error()
			} else if m.popup {
				return m, tea.Quit // close the popup; client is now on that session
			}
		}
	case "n": // start a new claude session (step 1: directory, step 2: name)
		m.mode = modeNew
		m.newStage = 0
		m.newDir = ""
		m.input.SetValue("")
		m.input.Placeholder = "directory for the new session (blank = home)"
		m.newHint = ""
		m.layoutModal()
		m.input.Focus()
		return m, textinput.Blink
	case "c": // threaded conversation popup
		if s, ok := m.current(); ok {
			m.openConvo(s)
			return m, textinput.Blink
		}
	case "r": // quick reply to a session you're not in
		if s, ok := m.current(); ok && s.TmuxPane != "" {
			m.mode = modeReply
			m.input.SetValue("")
			m.input.Placeholder = "type your reply, Enter to send, Esc to cancel"
			m.layoutModal()
			m.input.Focus()
			return m, textinput.Blink
		}
		m.status = "no tmux pane for this session"
	case "l":
		if s, ok := m.current(); ok {
			m.mode = modeLog
			m.layoutModal()
			m.vp.SetContent(m.historyView(s.ID))
			m.vp.GotoTop()
		}
	case "i": // open the interactive issue view for this repo's .scratch backlog
		if _, ok := m.current(); ok {
			m.openIssues()
			return m, nil
		}
		m.status = "no session selected"
	case "x": // gracefully quit a session (asks to confirm first)
		if s, ok := m.current(); ok && s.TmuxPane != "" {
			m.killID = s.ID
			m.mode = modeKill
			m.layoutModal()
		} else {
			m.status = "no tmux pane for this session"
		}
	case "R": // rename the selected session's tmux session
		if s, ok := m.current(); ok && s.TmuxPane != "" {
			m.mode = modeRename
			m.input.SetValue(m.label(s))
			m.input.CursorEnd()
			m.input.Placeholder = "new session name"
			m.layoutModal()
			m.input.Focus()
			return m, textinput.Blink
		}
		m.status = "no tmux pane for this session"
	}
	return m, nil
}

// handleRenameKey handles the rename prompt: Enter renames the tmux session,
// Esc cancels.
func (m model) handleRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "enter":
		newName := m.input.Value()
		if s, ok := m.current(); ok {
			if err := renameSession(s.TmuxPane, newName); err != nil {
				m.status = "rename failed: " + err.Error()
			} else {
				clean := sanitizeName(newName)
				m.status = "renamed to " + clean
				if m.paneSession == nil {
					m.paneSession = map[string]string{}
				}
				m.paneSession[s.TmuxPane] = clean // reflect immediately
			}
		}
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleKillKey handles the kill-confirmation prompt: y confirms a graceful
// quit, anything else cancels.
func (m model) handleKillKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		s, ok := m.sessionByID(m.killID)
		m.mode = modeNormal
		m.killID = ""
		if !ok || s.TmuxPane == "" {
			m.status = "session already gone"
			return m, nil
		}
		// Capture the window now — after /quit the claude pane is gone, but we
		// still want to tear down the whole window (sidebar included).
		_, window, _ := paneOrigin(s.TmuxPane)
		if err := gracefulExit(s.TmuxPane); err != nil {
			m.status = "quit failed: " + err.Error()
			return m, nil
		}
		m.status = "killing " + m.label(s) + "…"
		return m, killFallback(window, s.ID)
	default: // n, esc, or any other key cancels
		m.mode = modeNormal
		m.killID = ""
		return m, nil
	}
}

// killFallbackMsg fires after the grace period to tear down the killed session's
// whole tmux window (its claude pane plus the capataz sidebar).
type killFallbackMsg struct {
	window string
	id     string
}

// killFallback schedules the window teardown a few seconds after the graceful
// quit, giving Claude time to exit cleanly and write its transcript first.
func killFallback(window, id string) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return killFallbackMsg{window: window, id: id}
	})
}

func (m model) handleReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "enter":
		text := m.input.Value()
		if s, ok := m.current(); ok {
			if err := reply(s.TmuxPane, text); err != nil {
				m.status = "reply failed: " + err.Error()
			} else {
				m.status = "sent to " + short(s.Cwd)
			}
		}
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleNewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.newStage == 1 { // step back to the directory field
			m.newStage = 0
			m.input.SetValue(m.newDir)
			m.input.CursorEnd()
			m.input.Placeholder = "directory for the new session (blank = home)"
			m.newHint = ""
			return m, nil
		}
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	case "tab":
		if m.newStage != 0 { // completion only applies to the directory field
			return m, nil
		}
		completed, matches := completePath(m.input.Value())
		m.input.SetValue(completed)
		m.input.CursorEnd()
		switch {
		case len(matches) == 0:
			m.newHint = "no matching directories"
		case len(matches) > 1:
			m.newHint = "matches: " + strings.Join(firstN(matches, 8), "  ")
		default:
			m.newHint = ""
		}
		return m, nil
	case "enter":
		if m.newStage == 0 { // directory captured → advance to naming
			dir := strings.TrimSpace(m.input.Value())
			if dir == "" {
				dir, _ = os.UserHomeDir()
			}
			m.newDir = expandHome(dir)
			m.newStage = 1
			m.input.SetValue(sessionBase(m.newDir)) // sensible default name
			m.input.CursorEnd()
			m.input.Placeholder = "session name"
			m.newHint = ""
			return m, nil
		}
		// naming step → spawn. Creating a new session only needs tmux to be
		// reachable — not a current pane (a display-popup has no $TMUX_PANE).
		name := m.input.Value()
		m.mode = modeNormal
		m.input.Blur()
		if os.Getenv("TMUX") == "" {
			m.status = "can't spawn: capataz isn't in tmux"
			return m, nil
		}
		pane, err := newSessionWithSidebar(name, m.newDir, "")
		if err != nil {
			m.status = "new session failed: " + err.Error()
			return m, nil
		}
		if err := jump(pane); err != nil {
			m.status = "spawned but couldn't switch: " + err.Error()
			return m, nil
		}
		if m.popup {
			return m, tea.Quit // close the popup; client lands in the new session
		}
		return m, nil
	}
	m.newHint = ""
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// completePath does shell-style directory completion on a partial path. It
// returns the completed input (filled to the longest common prefix of matching
// directories, with a trailing slash when a single dir matches) and the list of
// matching directory names.
func completePath(in string) (string, []string) {
	trimmed := strings.TrimSpace(in)
	expanded := expandHome(trimmed)
	// filepath.Join (inside expandHome) drops a trailing slash, but the slash is
	// semantically important — it means "list inside this dir" — so restore it.
	switch {
	case expanded == "":
		h, _ := os.UserHomeDir()
		expanded = h + "/"
	case strings.HasSuffix(trimmed, "/") && !strings.HasSuffix(expanded, "/"):
		expanded += "/"
	}

	var dir, base string
	if strings.HasSuffix(expanded, "/") {
		dir, base = strings.TrimRight(expanded, "/"), ""
		if dir == "" {
			dir = "/"
		}
	} else {
		dir, base = filepath.Dir(expanded), filepath.Base(expanded)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return in, nil
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), base) {
			matches = append(matches, e.Name())
		}
	}
	if len(matches) == 0 {
		return in, nil
	}
	completed := filepath.Join(dir, longestCommonPrefix(matches))
	if len(matches) == 1 || base == "" {
		completed += "/" // descend into the single match, or stay inside the dir
	}
	return completed, matches
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

func firstN(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return append(ss[:n:n], "…")
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// openConvo enters the conversation view for a session.
func (m *model) openConvo(s proto.Session) {
	m.mode = modeConvo
	m.convoID = s.ID
	m.input.SetValue("")
	m.input.Placeholder = "type a message, Enter to send, Esc to close"
	m.layoutModal()
	m.input.Focus()
	m.refreshConvo(false)
	m.vp.GotoBottom()
}

// refreshConvo re-renders the open conversation. When follow is true the view
// sticks to the bottom only if it was already there, so scrolling up to read
// isn't yanked away by live updates.
func (m *model) refreshConvo(follow bool) {
	s, ok := m.sessionByID(m.convoID)
	if !ok {
		m.status = "session ended"
		m.mode = modeNormal
		m.input.Blur()
		return
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(renderTranscript(s.TranscriptPath, m.vp.Width-2))
	if !follow || atBottom {
		m.vp.GotoBottom()
	}
}

func (m *model) sessionByID(id string) (proto.Session, bool) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, true
		}
	}
	return proto.Session{}, false
}

func (m model) handleConvoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.convoID = ""
		m.input.Blur()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up":
		m.vp.LineUp(1)
		return m, nil
	case "down":
		m.vp.LineDown(1)
		return m, nil
	case "pgup", "ctrl+u":
		m.vp.HalfViewUp()
		return m, nil
	case "pgdown", "ctrl+d":
		m.vp.HalfViewDown()
		return m, nil
	case "enter":
		text := m.input.Value()
		if strings.TrimSpace(text) == "" {
			return m, nil
		}
		if s, ok := m.sessionByID(m.convoID); ok {
			if err := reply(s.TmuxPane, text); err != nil {
				m.status = "send failed: " + err.Error()
			} else {
				m.status = "sent"
				m.input.SetValue("")
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) historyView(id string) string {
	st, err := store.OpenReadOnly(paths.DB())
	if err != nil {
		return "No history database yet."
	}
	defer st.Close()
	recs, err := st.History(id, 100)
	if err != nil {
		return "Error reading history: " + err.Error()
	}
	if len(recs) == 0 {
		return "No recorded events for this session."
	}
	var b strings.Builder
	for _, r := range recs {
		t := time.Unix(0, r.TS).Format("15:04:05")
		line := fmt.Sprintf("%s  %-16s %-12s", t, r.State, r.Kind)
		if r.Summary != "" {
			line += "  " + r.Summary
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// ---- rendering ----

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true)
	okDot         = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●")
	badDot        = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("●")
)

func badge(s proto.State) string {
	var label string
	var color lipgloss.Color
	switch s {
	case proto.StateNeedsPermission:
		label, color = "NEEDS PERM", "196"
	case proto.StateWaiting:
		label, color = "WAITING", "214"
	case proto.StateAsking:
		label, color = "ASKING", "213"
	case proto.StateError:
		label, color = "ERROR", "196"
	case proto.StateWorking:
		label, color = "working", "39"
	case proto.StateDone:
		label, color = "done", "42"
	default:
		label, color = "ended", "240"
	}
	st := lipgloss.NewStyle().Foreground(color)
	if s.NeedsYou() {
		st = st.Bold(true)
	}
	return st.Render(fmt.Sprintf("%-11s", label))
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "starting capataz…"
	}
	base := m.dashboardView()
	switch m.mode {
	case modeConvo:
		return m.convoPopup(base)
	case modeLog:
		return m.scrollPopup(base, "Log · "+m.selTitle(), "PgUp/PgDn scroll · Esc close")
	case modeReply:
		return m.replyPopup(base)
	case modeNew:
		return m.newPopup(base)
	case modeHelp:
		return m.helpPopup(base)
	case modeKill:
		return m.killPopup(base)
	case modeRename:
		return m.renamePopup(base)
	case modeIssues:
		return m.issuesPopup(base)
	case modeStartWork:
		return m.startWorkPopup(base)
	}
	return base
}

// renamePopup is the session-rename input dialog.
func (m model) renamePopup(base string) string {
	pw, _, px, _ := m.popupDims()
	ph := 6
	py := (m.height - ph) / 2
	body := accentStyle.Render("› ") + m.input.View() + "\n" +
		dimStyle.Render("renames the tmux session ( . : spaces → _ )") + "\n" +
		footerStyle.Render("↵ rename · Esc cancel")
	box := panel("Rename session", body, pw, ph, true)
	return overlay(base, box, px, py)
}

// killPopup is the graceful-quit confirmation dialog.
func (m model) killPopup(base string) string {
	name := "this session"
	if s, ok := m.sessionByID(m.killID); ok {
		name = m.label(s)
	}
	lines := []string{
		"Quit " + accentStyle.Bold(true).Render(name) + "?",
		"",
		dimStyle.Render("Sends /quit so Claude exits cleanly and writes its"),
		dimStyle.Render("transcript. If it doesn't exit, the pane is force-killed."),
		"",
		footerStyle.Render("y quit · n / Esc cancel"),
	}
	body := strings.Join(lines, "\n")
	pw := 58
	if pw > m.width-2 {
		pw = m.width - 2
	}
	ph := len(lines) + 4
	if ph > m.height-2 {
		ph = m.height - 2
	}
	px := (m.width - pw) / 2
	py := (m.height - ph) / 2
	box := panel("Quit session", body, pw, ph, true)
	return overlay(base, box, px, py)
}

func (m model) selTitle() string {
	if s, ok := m.current(); ok {
		return m.label(s)
	}
	return ""
}

// dashboardView renders the stacked Needs You + Active + Detail panels and a
// bottom keybar, filling the popup's current width/height.
func (m model) dashboardView() string {
	w := m.width
	kb := m.keybarLines()
	bodyH := m.height - len(kb)
	if bodyH < 6 {
		bodyH = 6
	}

	needs, active := m.split()

	needsH := len(needs) + 2
	if needsH < 3 {
		needsH = 3
	}
	activeH := len(active) + 2
	if activeH < 3 {
		activeH = 3
	}

	// The Needs You + Active lists always get their full height (sessions are
	// never hidden). The leftover splits between Detail (sized to its content)
	// and an Issues panel that fills whatever remains — so the backlog lives in
	// the sidebar's spare space instead of behind a keypress.
	leftover := bodyH - needsH - activeH
	showExtra := leftover >= 6
	if !showExtra {
		leftover = 0
		activeH = bodyH - needsH
		if activeH < 3 {
			activeH = 3
		}
	}

	panels := []string{
		panel(fmt.Sprintf("Needs You (%d)", len(needs)), m.listBody(needs, w-2, true), w, needsH, m.cursorIn(needs)),
		panel(fmt.Sprintf("Active (%d)", len(active)), m.listBody(active, w-2, false), w, activeH, m.cursorIn(active)),
	}
	if showExtra {
		detailBody := m.detailBody(w-2, 0)
		// The inline Issues tracker is a sidebar-only glance; the popup is the
		// switch-and-work surface (press i there), so it keeps Detail full-height.
		showIssues := !m.popup
		detailH := leftover
		if showIssues {
			detailH = strings.Count(detailBody, "\n") + 3 // content lines + borders
			// Cap Detail so at least a few issue rows fit below it.
			if reserve := leftover - 5; detailH > reserve && reserve >= 5 {
				detailH = reserve
			}
			if detailH > leftover {
				detailH = leftover
			}
		}
		title := "Detail"
		if s, ok := m.current(); ok {
			title = "Detail · " + m.label(s)
		}
		panels = append(panels, panel(title, detailBody, w, detailH, false))

		if showIssues {
			if issuesH := leftover - detailH; issuesH >= 3 {
				it := "Issues"
				if m.issueRepoName != "" {
					it = "Issues · " + m.issueRepoName
				}
				panels = append(panels, panel(it, m.issuePanelBody(issuesH-2), w, issuesH, false))
			}
		}
	}
	stack := lipgloss.JoinVertical(lipgloss.Left, panels...)
	return stack + "\n" + strings.Join(kb, "\n")
}

// detailBody renders the selected session's context-window usage and a glance at
// what it last said — all from the session struct (no transcript parsing here).
func (m model) detailBody(innerW, rows int) string {
	sel, ok := m.current()
	if !ok {
		return dimStyle.Render("No session selected.")
	}
	div := dimStyle.Render(strings.Repeat("─", max(innerW, 1)))
	var b strings.Builder

	b.WriteString(badge(sel.State) + dimStyle.Render("  "+orDash(sel.TmuxPane)+" · "+age(sel.LastChange)) + "\n")
	b.WriteString(dimStyle.Render(truncate(sel.Cwd, innerW)) + "\n")
	if sel.Subagents > 0 {
		plur := ""
		if sel.Subagents != 1 {
			plur = "s"
		}
		b.WriteString(accentStyle.Render(fmt.Sprintf("⊂ %d subagent%s running", sel.Subagents, plur)) + "\n")
	}
	b.WriteString(div + "\n")

	// Context-window usage.
	if sel.ContextTokens > 0 {
		limit := contextLimit(sel.Model, sel.ContextTokens)
		pct := float64(sel.ContextTokens) / float64(limit)
		b.WriteString(headerStyle.Render("Context") + "\n")
		b.WriteString(bar(pct, innerW-7) + pctColor(int(pct*100)).Render(fmt.Sprintf(" %d%%", int(pct*100))) + "\n")
		b.WriteString(dimStyle.Render(fmtTokens(sel.ContextTokens)+" / "+fmtTokens(limit)) + "\n")
		if sel.OutTokens > 0 {
			b.WriteString(dimStyle.Render("last turn output: "+fmtTokens(sel.OutTokens)) + "\n")
		}
		if sel.Model != "" {
			b.WriteString(dimStyle.Render(sel.Model) + "\n")
		}
	} else {
		b.WriteString(dimStyle.Render("Context usage: no data yet") + "\n")
	}

	// Usage: account-wide plan limits (the Settings → Usage numbers).
	b.WriteString(div + "\n")
	b.WriteString(headerStyle.Render("Plan usage") + "\n")
	if m.limits.OK {
		switch m.limits.PlanType {
		case "api":
			b.WriteString(dimStyle.Render("Enterprise / API plan · billed per token") + "\n")
		default: // "pro" or legacy empty
			b.WriteString(usageLine("session", m.limits.SessionPct, nanoTime(m.limits.SessionReset)))
			b.WriteString(usageLine("weekly", m.limits.WeeklyPct, nanoTime(m.limits.WeeklyReset)))
		}
	} else {
		b.WriteString(dimStyle.Render("unavailable · check keychain / config") + "\n")
	}
	return b.String()
}

// usageLine renders one plan-limit row: label, bar, percent, and reset time.
func usageLine(label string, pct int, reset time.Time) string {
	line := lipgloss.NewStyle().Foreground(mutedColor).Render(fmt.Sprintf("%-8s", label)) +
		bar(float64(pct)/100, 8) +
		pctColor(pct).Render(fmt.Sprintf(" %d%%", pct))
	if r := resetStr(reset); r != "" {
		line += dimStyle.Render("  " + r)
	}
	return line + "\n"
}


func (m model) split() (needs, active []proto.Session) {
	for _, s := range m.sessions {
		if s.State.NeedsYou() {
			needs = append(needs, s)
		} else {
			active = append(active, s)
		}
	}
	return
}

func (m model) cursorIn(group []proto.Session) bool {
	for _, s := range group {
		if s.ID == m.selectedID {
			return true
		}
	}
	return false
}

// listBody renders the session rows for a panel. A magenta bar marks the
// selected row. emptyHint distinguishes the all-clear case for Needs You.
func (m model) listBody(group []proto.Session, innerW int, needsPanel bool) string {
	if len(group) == 0 {
		if needsPanel {
			return dimStyle.Render("  ✓ all caught up")
		}
		return dimStyle.Render("  —")
	}
	attachedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42")) // green ▸ for attached
	var lines []string
	for _, s := range group {
		// ▸ marks the session the user's tmux client is currently attached to.
		mark := " "
		if m.attachedSession != "" && m.paneSession[s.TmuxPane] == m.attachedSession {
			mark = attachedStyle.Render("▸")
		}
		// Trailing context-usage % (exactly 5 cells), colored by fill, kept last
		// so the ANSI doesn't disturb column alignment.
		ctx := dimStyle.Render("    —")
		if s.ContextTokens > 0 {
			pct := pctOf(s)
			ctx = pctColor(pct).Render(fmt.Sprintf("%4d%%", pct))
		}
		lbl := m.label(s)
		if s.Subagents > 0 {
			lbl = fmt.Sprintf("%s ⊂%d", lbl, s.Subagents) // running Task subagents
		}
		base := fmt.Sprintf("%s%s %-12s %4s ", mark, badge(s.State), truncate(lbl, 12), age(s.LastChange))
		cw := innerW - 6 // leave 5 for ctx + 1 for the prefix
		if cw < 1 {
			cw = 1
		}
		row := fitCell(base, cw) + ctx
		if s.ID == m.selectedID {
			lines = append(lines, accentBarStyle.Render("▎")+row)
		} else {
			lines = append(lines, " "+row)
		}
	}
	return strings.Join(lines, "\n")
}

// keybarLines builds the bottom footer: hint text left-aligned, connection dot
// right-aligned. The dot (● green/red) indicates daemon connection at a glance.
func (m model) keybarLines() []string {
	dot := badDot
	if m.connected {
		dot = okDot
	}
	width := m.width
	if width < 12 {
		width = 12
	}

	hint := keybarStyle.Render("↵ switch · n new · i issues · x kill · ? keys")
	hintW := ansi.StringWidth(hint)
	dotW := ansi.StringWidth(dot)
	// Left: one leading space + hint. Right: dot + one trailing space.
	// Fill the middle with spaces to push the dot to the right edge.
	gap := width - 1 - hintW - dotW - 1
	if gap < 1 {
		gap = 1
	}
	line := " " + hint + strings.Repeat(" ", gap) + dot + " "
	lines := []string{fitCell(line, width)}

	// Transient status (kill/reply/errors) on a second line. Suppress connection
	// chatter — the dot already conveys that.
	st := m.status
	switch st {
	case "connected", "connecting…", "reconnecting…":
		st = ""
	}
	if st != "" {
		lines = append(lines, fitCell("   "+dimStyle.Render(st), width))
	}
	return lines
}

// ---- floating popups ----

func (m model) popupDims() (pw, ph, px, py int) {
	pw = m.width - 8
	if pw > 96 {
		pw = 96
	}
	if pw < 24 || pw > m.width {
		pw = m.width
	}
	ph = m.height - 4
	if ph > 32 {
		ph = 32
	}
	if ph < 6 || ph > m.height {
		ph = m.height
	}
	px = (m.width - pw) / 2
	py = (m.height - ph) / 2
	return
}

// layoutModal sizes the viewport/input for whichever modal is (or will be) open.
func (m *model) layoutModal() {
	pw, ph, _, _ := m.popupDims()
	inner := pw - 2
	if inner < 1 {
		inner = 1
	}
	m.vp.Width = inner
	m.input.Width = inner - 2
	h := ph - 3 // border (2) + help (1)
	if m.mode == modeConvo {
		h = ph - 5 // border (2) + divider + input + help
	}
	if h < 1 {
		h = 1
	}
	m.vp.Height = h
}

func (m model) convoPopup(base string) string {
	pw, ph, px, py := m.popupDims()
	inner := pw - 2
	sel, _ := m.sessionByID(m.convoID)
	body := m.vp.View() + "\n" +
		dimStyle.Render(strings.Repeat("─", max(inner, 1))) + "\n" +
		accentStyle.Render("› ") + m.input.View() + "\n" +
		footerStyle.Render("PgUp/PgDn scroll · ↵ send · Esc close")
	box := panel("Conversation · "+m.label(sel)+"  ("+string(sel.State)+")", body, pw, ph, true)
	return overlay(base, box, px, py)
}

func (m model) scrollPopup(base, title, help string) string {
	pw, ph, px, py := m.popupDims()
	body := m.vp.View() + "\n" + footerStyle.Render(help)
	box := panel(title, body, pw, ph, true)
	return overlay(base, box, px, py)
}

func (m model) replyPopup(base string) string {
	pw, _, px, _ := m.popupDims()
	inner := pw - 2
	sel, _ := m.current()

	// Show the tail of the conversation so you know what you're replying to.
	convo := renderTranscript(sel.TranscriptPath, inner)
	all := strings.Split(strings.TrimRight(convo, "\n"), "\n")
	maxCtx := m.height - 8
	if maxCtx > 14 {
		maxCtx = 14
	}
	if maxCtx < 1 {
		maxCtx = 1
	}
	ctxN := len(all)
	if ctxN > maxCtx {
		ctxN = maxCtx
	}
	ph := ctxN + 5 // context + border(2) + divider + input + help
	if ph > m.height-2 {
		ph = m.height - 2
	}
	py := (m.height - ph) / 2

	body := tailLines(convo, ctxN) + "\n" +
		dimStyle.Render(strings.Repeat("─", max(inner, 1))) + "\n" +
		accentStyle.Render("› ") + m.input.View() + "\n" +
		footerStyle.Render("↵ send · Esc cancel")
	box := panel("Reply · "+m.label(sel)+"  ("+string(sel.State)+")", body, pw, ph, true)
	return overlay(base, box, px, py)
}

// tailLines returns at most the last n lines of s.
func tailLines(s string, n int) string {
	if n < 1 {
		n = 1
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (m model) helpPopup(base string) string {
	hdr := func(s string) string { return accentStyle.Bold(true).Render(s) }
	lines := []string{
		hdr("Navigation"),
		"  j / k   ↑ / ↓       move selection",
		"  gg / G              jump to top / bottom",
		"  Ctrl+d / Ctrl+u     half-page down / up",
		"",
		hdr("Sessions"),
		"  Enter / o           switch to the session (full screen)",
		"  n                   new claude session (dir, then name)",
		"  R                   rename the selected tmux session",
		"  r                   quick reply without leaving the dashboard",
		"  c                   conversation — threaded history",
		"  l                   event log",
		"  i                   issues — browse this repo's .scratch backlog",
		"                      (in issues: ↵ work · space select · p parallel · R review)",
		"  x                   quit a session (graceful, with confirm)",
		"",
		hdr("Other"),
		"  ?                   toggle this help",
		"  q / Esc             close (Esc dismisses the popup)",
	}
	body := strings.Join(lines, "\n") + "\n\n" + footerStyle.Render("  ? or Esc to close")

	pw := 62
	if pw > m.width-2 {
		pw = m.width - 2
	}
	ph := len(lines) + 4
	if ph > m.height-2 {
		ph = m.height - 2
	}
	px := (m.width - pw) / 2
	py := (m.height - ph) / 2
	box := panel("Keyboard shortcuts", body, pw, ph, true)
	return overlay(base, box, px, py)
}

func (m model) newPopup(base string) string {
	pw, _, px, _ := m.popupDims()
	ph := 6
	py := (m.height - ph) / 2

	title, help, defHint := "New session · directory", "Tab complete · ↵ next · Esc cancel", "Tab to complete the directory"
	if m.newStage == 1 {
		title, help, defHint = "New session · name", "↵ create claude · Esc back", "session name; defaults to the folder name"
	}
	hint := footerStyle.Render(m.newHint)
	if m.newHint == "" {
		hint = dimStyle.Render(defHint)
	}
	body := accentStyle.Render("› ") + m.input.View() + "\n" +
		hint + "\n" +
		footerStyle.Render(help)
	box := panel(title, body, pw, ph, true)
	return overlay(base, box, px, py)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func age(nanos int64) string {
	if nanos == 0 {
		return "-"
	}
	d := time.Since(time.Unix(0, nanos))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func short(cwd string) string {
	if cwd == "" {
		return "?"
	}
	return filepath.Base(cwd)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}
