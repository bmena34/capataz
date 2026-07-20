package tui

import (
	"encoding/json"
	"net"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/proto"
)

// pushMsg carries a live update from the daemon into the Bubble Tea loop.
type pushMsg struct{ push proto.Push }

// connMsg reports daemon connection state changes.
type connMsg struct {
	connected bool
	text      string
}

// listen dials the daemon, subscribes, and streams pushes onto ch forever,
// reconnecting on failure. Runs in its own goroutine.
func listen(ch chan tea.Msg) {
	for {
		conn, err := net.Dial("unix", paths.Sock())
		if err != nil {
			ch <- connMsg{connected: false, text: "daemon not running — run `capataz install`"}
			time.Sleep(time.Second)
			continue
		}
		if err := json.NewEncoder(conn).Encode(proto.Msg{Type: "subscribe"}); err != nil {
			conn.Close()
			time.Sleep(time.Second)
			continue
		}
		ch <- connMsg{connected: true}

		dec := json.NewDecoder(conn)
		for {
			var p proto.Push
			if err := dec.Decode(&p); err != nil {
				break
			}
			ch <- pushMsg{push: p}
		}
		conn.Close()
		ch <- connMsg{connected: false, text: "reconnecting…"}
		time.Sleep(time.Second)
	}
}

// dismissSession asks the daemon to drop a retained (ended) session from the
// resume list: a one-shot dial that sends a "dismiss" command. The daemon
// deletes it from the store and broadcasts a "remove" so every dashboard drops
// the row.
func dismissSession(id string) error {
	conn, err := net.Dial("unix", paths.Sock())
	if err != nil {
		return err
	}
	defer conn.Close()
	return json.NewEncoder(conn).Encode(proto.Msg{Type: "dismiss", SessionID: id})
}

// retainedSessions returns the daemon's retained (ended, resumable) sessions,
// most recently active first. A one-shot dial: subscribe, read the initial
// snapshot push, hang up. Returns nil when the daemon is unreachable or slow —
// callers treat that as "nothing to resume" rather than blocking startup.
func retainedSessions() []proto.Session {
	conn, err := net.DialTimeout("unix", paths.Sock(), time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(proto.Msg{Type: "subscribe"}); err != nil {
		return nil
	}
	var p proto.Push
	if err := json.NewDecoder(conn).Decode(&p); err != nil {
		return nil
	}
	var ended []proto.Session
	for _, s := range p.Sessions {
		if s.State == proto.StateEnded && s.ID != "" {
			ended = append(ended, s)
		}
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].LastChange > ended[j].LastChange })
	return ended
}

// waitForMsg yields the next message from the listen goroutine's channel.
func waitForMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}
