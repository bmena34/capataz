// Package daemon runs the always-on background process. Hooks connect to its
// Unix socket to report events; the TUI connects to subscribe to live state.
package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/logging"
	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/proto"
	"github.com/bmena34/capataz/internal/store"
)

type subscriber struct {
	ch chan proto.Push
}

type hub struct {
	mu       sync.Mutex
	sessions map[string]*proto.Session
	subs     map[*subscriber]struct{}
	store    *store.Store
	usage    proto.Usage // latest plan usage, polled centrally and pushed
	cfg      config.Config
}

// Run starts the daemon. It blocks until the process is killed.
func Run() error {
	if err := paths.Ensure(); err != nil {
		return err
	}

	// If a daemon is already listening, do nothing.
	if c, err := net.DialTimeout("unix", paths.Sock(), 200*time.Millisecond); err == nil {
		c.Close()
		logging.Infof("daemon already running; exiting")
		return nil
	}
	// Remove a stale socket file left by a previous crash.
	_ = os.Remove(paths.Sock())

	st, err := store.Open(paths.DB())
	if err != nil {
		logging.Errorf("open store: %v", err)
		return err
	}
	defer st.Close()

	h := &hub{
		sessions: map[string]*proto.Session{},
		subs:     map[*subscriber]struct{}{},
		store:    st,
		cfg:      config.Load(),
	}
	h.reload()
	if h.cfg.Usage.Enabled {
		go h.pollUsage() // central plan-usage poller; pushes to all dashboards
	} else {
		logging.Infof("usage polling disabled via config")
	}
	go h.pruneLoop() // drop sessions whose tmux pane has died

	ln, err := net.Listen("unix", paths.Sock())
	if err != nil {
		logging.Errorf("listen on %s: %v", paths.Sock(), err)
		return err
	}
	defer ln.Close()
	logging.Infof("daemon listening on %s", paths.Sock())

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go h.serve(conn)
	}
}

// reload repopulates sessions from the store on startup so a daemon restart
// doesn't drop sessions from the dashboard. Ended (resumable) rows are kept as
// they are; live-paned sessions stay live; a paned session whose tmux pane died
// while the daemon was down is marked ended (retained, not dropped). Nothing is
// deleted on reload — trimming of old ended rows is left to TrimEnded.
func (h *hub) reload() {
	saved, err := h.store.LoadSessions()
	if err != nil {
		logging.Errorf("reload sessions: %v", err)
		return
	}
	alive := livePanes()
	kept := 0
	for _, s := range saved {
		sc := s
		if sc.State != proto.StateEnded && (sc.TmuxPane == "" || (alive != nil && !alive[sc.TmuxPane])) {
			// Pane died while the daemon was down — retain as ended, not drop.
			sc.State = proto.StateEnded
			sc.TmuxPane = ""
			_ = h.store.SaveSession(sc)
		}
		h.sessions[sc.ID] = &sc
		kept++
	}
	h.trimEnded()
	logging.Infof("reloaded %d session(s) from store", kept)
	count, color := h.needsSummaryLocked()
	h.updateBadge(count, color)
}

// livePanes returns the set of existing tmux pane ids, or nil if tmux can't be
// queried (in which case callers should not prune).
func livePanes() map[string]bool {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, ln := range strings.Split(string(out), "\n") {
		if p := strings.TrimSpace(ln); p != "" {
			set[p] = true
		}
	}
	return set
}

// paneSessionName returns the tmux session name that owns a pane, or "" if tmux
// can't resolve it (pane gone, tmux unreachable). Used to capture a session's
// display name while its pane is live so a resumed row keeps it.
func paneSessionName(pane string) string {
	if pane == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// pruneInterval is how often the daemon sweeps for sessions whose pane has died.
const pruneInterval = 5 * time.Second

// pruneLoop periodically finds sessions whose tmux pane no longer exists (killed
// from the dashboard, crashed, or closed without a SessionEnd) and transitions
// them to the ended (resumable) state rather than deleting them, so the row
// survives a tmux/machine restart. Already-ended rows are skipped so they aren't
// re-processed every sweep.
func (h *hub) pruneLoop() {
	for {
		time.Sleep(pruneInterval)
		alive := livePanes()
		if alive == nil {
			continue // tmux unreachable — don't prune blindly
		}
		h.mu.Lock()
		var ended []proto.Session
		for _, s := range h.sessions {
			if s.State == proto.StateEnded {
				continue // already ended — don't re-sweep
			}
			if s.TmuxPane == "" || !alive[s.TmuxPane] {
				s.State = proto.StateEnded
				s.TmuxPane = ""
				ended = append(ended, *s)
			}
		}
		count, color := h.needsSummaryLocked()
		h.mu.Unlock()

		for _, snap := range ended {
			_ = h.store.SaveSession(snap)
			logging.Infof("swept dead pane, ended session %s", shortID(snap.ID))
			s := snap
			h.broadcast(proto.Push{Type: "update", Session: &s})
		}
		if len(ended) > 0 {
			h.updateBadge(count, color)
			h.trimEnded()
		}
	}
}

// trimEnded caps the retained ended rows in the store AND evicts the same rows
// from the in-memory h.sessions map, so the two never diverge on a live daemon.
// TrimEnded returns the ids it deleted (the over-cap, least-recent ended rows);
// we then remove those ids from the map under h.mu and broadcast a "remove" for
// each so every dashboard drops the stale row — mirroring handleDismiss. Store
// write and broadcast run outside the lock, matching handleEvent/pruneLoop.
func (h *hub) trimEnded() {
	deleted, err := h.store.TrimEnded(h.cfg.Resume.MaxRetained)
	if err != nil {
		logging.Errorf("trim ended: %v", err)
		return
	}
	if len(deleted) == 0 {
		return
	}
	h.mu.Lock()
	var removed []proto.Session
	for _, id := range deleted {
		if s := h.sessions[id]; s != nil {
			removed = append(removed, *s)
			delete(h.sessions, id)
		}
	}
	count, color := h.needsSummaryLocked()
	h.mu.Unlock()

	if len(removed) == 0 {
		return
	}
	h.updateBadge(count, color)
	for i := range removed {
		s := removed[i]
		logging.Infof("trimmed ended session %s (over cap)", shortID(s.ID))
		h.broadcast(proto.Push{Type: "remove", Session: &s})
	}
}

func (h *hub) serve(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)

	var m proto.Msg
	if err := dec.Decode(&m); err != nil {
		return
	}

	switch m.Type {
	case "event":
		if m.Event != nil {
			h.handleEvent(*m.Event)
		}
	case "subscribe":
		h.handleSubscribe(conn)
	case "dismiss":
		if m.SessionID != "" {
			h.handleDismiss(m.SessionID)
		}
	}
}

// handleDismiss drops a retained (ended) session from the resume list: remove
// it from the in-memory map and the store, then broadcast a "remove" so every
// dashboard forgets the row. Only ended sessions are dismissable — dismissing a
// live session would just orphan its pane, so those are left to the kill path.
func (h *hub) handleDismiss(id string) {
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil || s.State != proto.StateEnded {
		h.mu.Unlock()
		return
	}
	snap := *s
	delete(h.sessions, id)
	count, color := h.needsSummaryLocked()
	h.mu.Unlock()

	_ = h.store.DeleteSession(id)
	logging.Infof("dismissed ended session %s", shortID(id))
	h.updateBadge(count, color)
	h.broadcast(proto.Push{Type: "remove", Session: &snap})
}

func (h *hub) handleSubscribe(conn net.Conn) {
	sub := &subscriber{ch: make(chan proto.Push, 128)}

	h.mu.Lock()
	usage := h.usage
	snapshot := proto.Push{Type: "snapshot", Sessions: h.sortedLocked(), Usage: &usage}
	h.subs[sub] = struct{}{}
	h.mu.Unlock()

	logging.Infof("tui subscribed (%d sessions in snapshot)", len(snapshot.Sessions))
	enc := json.NewEncoder(conn)
	if err := enc.Encode(snapshot); err != nil {
		h.removeSub(sub)
		return
	}
	for push := range sub.ch {
		if err := enc.Encode(push); err != nil {
			break
		}
	}
	h.removeSub(sub)
	logging.Infof("tui unsubscribed")
}

// shortID trims a session UUID for readable logs.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (h *hub) removeSub(sub *subscriber) {
	h.mu.Lock()
	if _, ok := h.subs[sub]; ok {
		delete(h.subs, sub)
		close(sub.ch)
	}
	h.mu.Unlock()
}

func (h *hub) handleEvent(e proto.Event) {
	now := time.Now().UnixNano()

	h.mu.Lock()
	s := h.sessions[e.SessionID]
	if s == nil {
		// capataz only tracks tmux sessions (the ones it can act on). A brand-new
		// session with no tmux pane is detached (a claude run outside tmux), so we
		// ignore it entirely rather than show an un-actionable row.
		if e.TmuxPane == "" {
			h.mu.Unlock()
			return
		}
		s = &proto.Session{ID: e.SessionID, Started: now}
		h.sessions[e.SessionID] = s
	}
	if e.Cwd != "" {
		s.Cwd = e.Cwd
	}
	if e.TmuxPane != "" {
		s.TmuxPane = e.TmuxPane
		// Capture the tmux session name while the pane is live, so an ended
		// (empty-pane) row can later be resumed under its former (possibly
		// renamed) label rather than the cwd basename. Best-effort: keep the
		// prior name if the lookup fails.
		if name := paneSessionName(e.TmuxPane); name != "" {
			s.Name = name
		}
	}
	if e.TranscriptPath != "" {
		s.TranscriptPath = e.TranscriptPath
	}
	s.LastEvent = string(e.Kind)
	s.LastChange = now

	prev := s.State
	state := mapState(e.Kind)
	switch e.Kind {
	case proto.KindStop:
		summary, asking, u := analyzeTranscript(e.TranscriptPath)
		if summary != "" {
			s.Summary = summary
		}
		applyUsage(s, u)
		if asking {
			state = proto.StateAsking
		} else {
			state = proto.StateDone
		}
	case proto.KindPermission, proto.KindIdle:
		// Refresh usage at "needs you" moments without changing the summary.
		applyUsage(s, latestUsage(e.TranscriptPath))
	case proto.KindWorking:
		// Keep the context gauge near-live: refresh usage on every working tick
		// (UserPromptSubmit / PreToolUse) via a cheap tail-read.
		applyUsage(s, latestUsage(e.TranscriptPath))
	}

	// Subagent fan-out: count Task spawns, decrement when one finishes, and reset
	// at the turn's end so a missed SubagentStop can't wedge the count.
	switch {
	case e.Kind == proto.KindSubagentStop:
		if s.Subagents > 0 {
			s.Subagents--
		}
		state = prev // a subagent finishing doesn't change the parent's state
	case e.Kind == proto.KindWorking && (e.ToolName == "Agent" || e.ToolName == "Task"):
		// "Agent" is the current subagent-spawning tool; "Task" is the older name.
		s.Subagents++
	case e.Kind == proto.KindStop || e.Kind == proto.KindStopFailure || e.Kind == proto.KindSessionEnd:
		s.Subagents = 0
	}

	if state != prev { // state actually changed → reset the in-state timer
		s.StateSince = now
	}
	s.State = state

	if e.Kind == proto.KindSessionEnd {
		// Retain the session as a resumable (ended) row rather than deleting it,
		// so it survives a tmux/machine restart. State==ended + empty pane is the
		// resumable marker.
		s.State = proto.StateEnded
		s.TmuxPane = ""
	}
	snap := *s
	needsCount, needsColor := h.needsSummaryLocked()
	h.mu.Unlock()

	// Ambient awareness: keep the tmux needs-you badge current.
	h.updateBadge(needsCount, needsColor)

	_ = h.store.Insert(store.Record{
		TS: now, SessionID: e.SessionID, Kind: string(e.Kind),
		State: string(state), Cwd: snap.Cwd, Summary: snap.Summary,
	})
	_ = h.store.SaveSession(snap)
	logging.Infof("event kind=%s state=%s session=%s pane=%q cwd=%s",
		e.Kind, state, shortID(e.SessionID), snap.TmuxPane, snap.Cwd)
	h.broadcast(proto.Push{Type: "update", Session: &snap})
	if e.Kind == proto.KindSessionEnd {
		h.trimEnded()
	}
}

func (h *hub) broadcast(p proto.Push) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.ch <- p:
		default:
			// Slow subscriber; drop rather than block the daemon.
		}
	}
}

// sortedLocked returns sessions ordered needs-you-first. Caller holds the lock.
func (h *hub) sortedLocked() []proto.Session {
	out := make([]proto.Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := out[i].State.Priority(), out[j].State.Priority()
		if pi != pj {
			return pi < pj
		}
		return out[i].LastChange > out[j].LastChange
	})
	return out
}

// applyUsage updates a session's token fields when usage data is present.
func applyUsage(s *proto.Session, u usageInfo) {
	if u.ContextTokens > 0 {
		s.ContextTokens = u.ContextTokens
		s.OutTokens = u.OutTokens
	}
	if u.Model != "" {
		s.Model = u.Model
	}
}

func mapState(k proto.Kind) proto.State {
	switch k {
	case proto.KindPermission:
		return proto.StateNeedsPermission
	case proto.KindIdle:
		return proto.StateWaiting
	case proto.KindStopFailure:
		return proto.StateError
	case proto.KindSessionEnd:
		return proto.StateEnded
	default: // session_start, working
		return proto.StateWorking
	}
}

// usageInfo is usage derived from a transcript: the latest turn's context size.
type usageInfo struct {
	ContextTokens int
	OutTokens     int
	Model         string
}

// turnUsage is one assistant turn's raw token counts.
type turnUsage struct {
	in, out, cacheRead, cacheWrite int
	model                          string
}

// transcriptTail is how much of the end of a transcript we read. Transcripts
// grow to many MB on long sessions, but only the last turn or two matters, so we
// tail-read a bounded window — cheap enough to refresh on every working event.
const transcriptTail = 512 * 1024

// tailBytes returns up to max bytes from the end of a file. The first line in
// the result may be partial (it's the oldest and gets skipped by JSON parsing).
func tailBytes(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > max {
		if _, err := f.Seek(-max, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

// latestUsage returns the most recent assistant turn's token usage from the tail
// of a transcript. Cheap (tail-read + early break) so it can run every tick.
func latestUsage(path string) usageInfo {
	var u usageInfo
	if path == "" {
		return u
	}
	data, err := tailBytes(path, transcriptTail)
	if err != nil {
		return u
	}
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if t, _ := rec["type"].(string); t != "assistant" {
			continue
		}
		tu := parseTurnUsage(rec)
		if tu.in+tu.cacheRead+tu.cacheWrite == 0 {
			continue // assistant record without usage — keep looking back
		}
		u.ContextTokens = tu.in + tu.cacheRead + tu.cacheWrite
		u.OutTokens = tu.out
		u.Model = tu.model
		break
	}
	return u
}

// analyzeTranscript reads the tail of a Claude Code transcript (JSONL) and
// returns a short summary of the last assistant message, whether it looks like a
// question, and the latest turn's context size. It scans from the end and stops
// as soon as it has both.
func analyzeTranscript(path string) (summary string, asking bool, u usageInfo) {
	if path == "" {
		return "", false, u
	}
	data, err := tailBytes(path, transcriptTail)
	if err != nil {
		return "", false, u
	}
	lines := bytes.Split(data, []byte("\n"))
	var text string
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if t, _ := rec["type"].(string); t != "assistant" {
			continue
		}
		if u.ContextTokens == 0 {
			tu := parseTurnUsage(rec)
			u.ContextTokens = tu.in + tu.cacheRead + tu.cacheWrite
			u.OutTokens = tu.out
			u.Model = tu.model
		}
		if text == "" {
			text = extractText(rec)
		}
		if text != "" && u.ContextTokens != 0 {
			break // have the most recent turn's text + usage
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, u
	}
	return oneLine(text, 280), strings.HasSuffix(text, "?"), u
}

// parseTurnUsage pulls one assistant turn's token counts from its record.
func parseTurnUsage(rec map[string]any) turnUsage {
	msg, ok := rec["message"].(map[string]any)
	if !ok {
		return turnUsage{}
	}
	model, _ := msg["model"].(string)
	usage, ok := msg["usage"].(map[string]any)
	if !ok {
		return turnUsage{model: model}
	}
	num := func(k string) int {
		if f, ok := usage[k].(float64); ok {
			return int(f)
		}
		return 0
	}
	return turnUsage{
		in:         num("input_tokens"),
		out:        num("output_tokens"),
		cacheRead:  num("cache_read_input_tokens"),
		cacheWrite: num("cache_creation_input_tokens"),
		model:      model,
	}
}

// extractText pulls concatenated text content out of an assistant transcript
// record, tolerating schema variations by walking generic maps.
func extractText(rec map[string]any) string {
	msg, ok := rec["message"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := msg["content"].([]any)
	if !ok {
		// content may be a plain string.
		if s, ok := msg["content"].(string); ok {
			return s
		}
		return ""
	}
	var b strings.Builder
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "text" {
			if s, ok := block["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// oneLine collapses text to a single trimmed line capped at n runes.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		// Prefer the last non-empty line — that's usually the actual question.
		parts := strings.Split(s, "\n")
		for j := len(parts) - 1; j >= 0; j-- {
			if p := strings.TrimSpace(parts[j]); p != "" {
				s = p
				break
			}
		}
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
