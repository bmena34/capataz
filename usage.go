package main

// capataz usage — headless plan-usage gauge.
//
// Reads the Claude Code OAuth token from the macOS Keychain (same path the
// daemon uses) and calls the configured usage endpoint. This is an UNOFFICIAL
// Anthropic endpoint (the same one `claude`'s /usage command hits). It is not
// publicly documented and can change or disappear — hence the URL and keychain
// service name are configurable, and config.Usage.Enabled lets you turn the
// whole feature off.
//
// The fetch logic below mirrors internal/daemon/usage.go. The daemon functions
// are unexported (they run inside a long-lived process), so this command
// replicates the minimal subset needed for a one-shot CLI call rather than
// refactoring the daemon package.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/bmena34/capataz/internal/config"
)

// runUsage fetches the plan-usage gauge and prints it to stdout.
// If config.Usage.Enabled is false it prints a note and returns nil.
func runUsage() error {
	cfg := config.Load()
	if !cfg.Usage.Enabled {
		fmt.Println("usage: disabled (set usage.enabled=true in", "~/.config/capataz/config.json)")
		return nil
	}

	u, err := usageFetch(cfg.Usage)
	if err != nil {
		return fmt.Errorf("usage: %w", err)
	}

	printUsageGauge(u)
	return nil
}

// usageFetch reads the OAuth token from the Keychain and calls the usage
// endpoint, returning a parsed result. Mirrors daemon.fetchUsage.
func usageFetch(cfg config.Usage) (usageResult, error) {
	token := usageKeychainToken(cfg.KeychainService)
	if token == "" {
		return usageResult{}, fmt.Errorf("no OAuth token found in keychain (service %q)", cfg.KeychainService)
	}

	req, err := http.NewRequest("GET", cfg.URL, nil)
	if err != nil {
		return usageResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return usageResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return usageResult{}, fmt.Errorf("usage endpoint returned %d", resp.StatusCode)
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
		return usageResult{}, err
	}

	r := usageResult{}
	matched := false
	for _, l := range data.Limits {
		switch l.Kind {
		case "session":
			r.SessionPct, r.SessionReset, matched = int(l.Percent+0.5), usageParseRFC(l.ResetsAt), true
		case "weekly_all":
			r.WeeklyPct, r.WeeklyReset = int(l.Percent+0.5), usageParseRFC(l.ResetsAt)
		}
	}
	if !matched {
		if data.FiveHour.Utilization > 0 || data.SevenDay.Utilization > 0 {
			r.SessionPct = int(data.FiveHour.Utilization + 0.5)
			r.SessionReset = usageParseRFC(data.FiveHour.ResetsAt)
			r.WeeklyPct = int(data.SevenDay.Utilization + 0.5)
			r.WeeklyReset = usageParseRFC(data.SevenDay.ResetsAt)
			r.PlanType = "pro"
		} else {
			r.PlanType = "api"
		}
	} else {
		r.PlanType = "pro"
	}
	if cfg.PlanType == "pro" || cfg.PlanType == "api" {
		r.PlanType = cfg.PlanType
	}
	return r, nil
}

// usageResult holds the parsed plan-usage data.
type usageResult struct {
	PlanType     string    // "pro" | "api" | ""
	SessionPct   int       // 5-hour session utilization 0–100
	SessionReset time.Time // when the session window resets
	WeeklyPct    int       // 7-day weekly utilization 0–100
	WeeklyReset  time.Time // when the weekly window resets
}

// usageKeychainToken reads the Claude Code OAuth access token from the macOS
// Keychain. Mirrors daemon.keychainToken.
func usageKeychainToken(service string) string {
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

// usageParseRFC parses an RFC3339 reset timestamp, returning zero on failure.
func usageParseRFC(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// printUsageGauge prints a concise plain-text gauge to stdout.
func printUsageGauge(u usageResult) {
	switch u.PlanType {
	case "api":
		fmt.Println("Plan: Enterprise / API (billed per token — no rate-limit bars)")
	default: // "pro" or legacy empty
		fmt.Println("Plan: Pro (rate-limited)")
		fmt.Println()
		fmt.Printf("Session  %s %3d%%  %s\n",
			usageBar(u.SessionPct, 20),
			u.SessionPct,
			usageResetStr(u.SessionReset))
		fmt.Printf("Weekly   %s %3d%%  %s\n",
			usageBar(u.WeeklyPct, 20),
			u.WeeklyPct,
			usageResetStr(u.WeeklyReset))
	}
}

// usageBar renders a plain-ASCII [████░░░░] bar of the given width.
func usageBar(pct, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct * width) / 100
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
}

// usageResetStr formats when a rate-limit window resets.
// Relative when under a day ("in 4h30m"), absolute otherwise ("Wed 3:59pm").
func usageResetStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Until(t)
	if d <= 0 {
		return "resetting…"
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("resets in %dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return "resets " + t.Format("Mon 3:04pm")
}
