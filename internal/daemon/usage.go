// Live plan-usage polling. The daemon fetches the account's Settings → Usage
// numbers once a minute and pushes them to every connected dashboard, so any
// number of sidebars share a single API call instead of each polling.
package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/logging"
	"github.com/bmena34/capataz/internal/proto"
)

// keychainToken reads Claude Code's OAuth access token from the macOS Keychain.
func keychainToken(service string) string {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return ""
	}
	var c struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(out, &c) != nil {
		return ""
	}
	return c.ClaudeAiOauth.AccessToken
}

// fetchUsage calls the configured usage endpoint (the same one /usage uses) and
// returns the session + weekly limits as a proto.Usage.
func fetchUsage(cfg config.Usage) (proto.Usage, error) {
	token := keychainToken(cfg.KeychainService)
	if token == "" {
		return proto.Usage{}, fmt.Errorf("no OAuth token in keychain")
	}
	req, err := http.NewRequest("GET", cfg.URL, nil)
	if err != nil {
		return proto.Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return proto.Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return proto.Usage{}, fmt.Errorf("usage endpoint returned %d", resp.StatusCode)
	}

	var data struct {
		Limits []struct {
			Kind     string  `json:"kind"`
			Percent  float64 `json:"percent"`
			ResetsAt string  `json:"resets_at"`
		} `json:"limits"`
		FiveHour struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return proto.Usage{}, err
	}

	u := proto.Usage{OK: true}
	matched := false
	for _, l := range data.Limits { // prefer the clean limits[] array
		switch l.Kind {
		case "session":
			u.SessionPct, u.SessionReset, matched = int(l.Percent+0.5), rfcNanos(l.ResetsAt), true
		case "weekly_all":
			u.WeeklyPct, u.WeeklyReset = int(l.Percent+0.5), rfcNanos(l.ResetsAt)
		}
	}
	if !matched {
		if data.FiveHour.Utilization > 0 || data.SevenDay.Utilization > 0 {
			// Older response shape — still rate-limited.
			u.SessionPct = int(data.FiveHour.Utilization + 0.5)
			u.SessionReset = rfcNanos(data.FiveHour.ResetsAt)
			u.WeeklyPct = int(data.SevenDay.Utilization + 0.5)
			u.WeeklyReset = rfcNanos(data.SevenDay.ResetsAt)
			u.PlanType = "pro"
		} else {
			// No rate-limit data at all — likely enterprise / API billing.
			u.PlanType = "api"
		}
	} else {
		u.PlanType = "pro"
	}
	// Config override wins.
	if cfg.PlanType == "pro" || cfg.PlanType == "api" {
		u.PlanType = cfg.PlanType
	}
	return u, nil
}

// rfcNanos parses an RFC3339 timestamp to unix nanos (0 if empty/invalid).
func rfcNanos(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}

// pollUsage fetches usage immediately, then on the configured interval, storing
// the latest on the hub and broadcasting it to all subscribers.
func (h *hub) pollUsage() {
	interval := time.Duration(h.cfg.Usage.PollSeconds) * time.Second
	for {
		u, err := fetchUsage(h.cfg.Usage)
		if err != nil {
			logging.Infof("usage poll: %v", err)
		} else {
			h.mu.Lock()
			h.usage = u
			h.mu.Unlock()
			h.broadcast(proto.Push{Type: "usage", Usage: &u})
		}
		time.Sleep(interval)
	}
}
