// Package usageview is the plan-usage + context popup behind
// `capataz usage-view` (the herdr plugin's usage pane).
//
// It renders two things in one pane:
//
//   - Plan usage — whatever `capataz usage` prints (the Keychain-backed plan
//     gauge). A failure becomes a one-line note; the context gauges still work.
//   - Context — one gauge per herdr agent pane, showing how full that agent's
//     context window is. Sessions are matched to transcripts exactly, via the
//     session id herdr reports in `herdr agent list`, and read straight off disk.
//
// Usage:
//
//	capataz usage-view [flags]
//
// Flags:
//
//	-interval      duration  context refresh cadence (default 2s)
//	-plan-interval duration  plan-usage refresh cadence (default 1m)
//	-once                    render a single frame and exit (implied when not a TTY)
//	-no-plan                 skip the plan gauge; show context only
//	-limit         string    context window override, e.g. "1m" or "200k"
//	-report                  run headless: push each pane's context gauge to herdr
//	                         as pane-metadata token "context" (sidebar "$context")
//
// Environment:
//
//	CAPATAZ_BIN            path to the capataz binary (default: this executable)
//	HERDR_BIN              path to the herdr binary   (default "herdr")
//	CAPATAZ_CONTEXT_LIMIT  same as -limit
//	CLAUDE_CONFIG_DIR      Claude Code state dir (default ~/.claude)
//	NO_COLOR               disable ANSI color
package usageview

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Run parses args and runs the command; it is `capataz usage-view`.
func Run(args []string) error {
	fs := flag.NewFlagSet("usage-view", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	interval := fs.Duration("interval", 2*time.Second, "context refresh cadence")
	planInterval := fs.Duration("plan-interval", time.Minute, "plan-usage refresh cadence")
	once := fs.Bool("once", false, "render a single frame and exit")
	noPlan := fs.Bool("no-plan", false, "skip the plan gauge; show context only")
	limit := fs.String("limit", "", `context window override, e.g. "1m" or "200k"`)
	report := fs.Bool("report", false, "push context gauges to herdr pane metadata instead of rendering")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: capataz usage-view [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n")
		fmt.Fprintf(os.Stderr, "  CAPATAZ_BIN            path to capataz binary (default: this executable)\n")
		fmt.Fprintf(os.Stderr, "  HERDR_BIN              path to herdr binary   (default \"herdr\")\n")
		fmt.Fprintf(os.Stderr, "  CAPATAZ_CONTEXT_LIMIT  same as -limit\n")
		fmt.Fprintf(os.Stderr, "  CLAUDE_CONFIG_DIR      Claude Code state dir (default ~/.claude)\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval < 250*time.Millisecond {
		*interval = 250 * time.Millisecond // guard against a busy-loop refresh
	}

	capatazBin := selfBin()
	herdrBin := os.Getenv("HERDR_BIN")
	if herdrBin == "" {
		herdrBin = "herdr"
	}

	// -report is the headless daemon mode behind the sidebar's "$context" token:
	// no TTY, no frames — just sweep-and-push until herdr goes away.
	if *report {
		return runReport(&realRunner{herdrBin: herdrBin}, newLimitResolver(*limit), claudeDir(), *interval, os.Stderr)
	}

	selfCwd, _ := os.Getwd() // best-effort: only used to mark the originating pane

	a := &app{
		runner:       &realRunner{capatazBin: capatazBin, herdrBin: herdrBin},
		lim:          newLimitResolver(*limit),
		dir:          claudeDir(),
		selfCwd:      selfCwd,
		interval:     *interval,
		planInterval: *planInterval,
		showPlan:     !*noPlan,
		color:        os.Getenv("NO_COLOR") == "" && isTTY(os.Stdout),
	}

	// Without a terminal there is nothing to refresh into and no key to quit
	// with, so piped/redirected output degrades to a single frame.
	if *once || !isTTY(os.Stdout) || !isTTY(os.Stdin) {
		return a.runOnce(os.Stdout)
	}
	return a.runLive(os.Stdout)
}

// app is the view's running state: its dependencies plus the most recent plan
// gauge, which is fetched off the render path because it makes a network call.
type app struct {
	runner       Runner
	lim          *limitResolver
	dir          string // Claude Code state dir
	selfCwd      string // cwd this popup was opened in (the originating pane's repo)
	interval     time.Duration
	planInterval time.Duration
	showPlan     bool
	color        bool

	mu       sync.Mutex
	plan     string
	planNote string
}

// snapshot builds one frame's View. Context comes from local file reads, so this
// is cheap enough to run on every tick; the plan gauge is whatever the async
// fetch last stored.
func (a *app) snapshot(live bool) View {
	v := View{Live: live, Interval: a.interval, ShowPlan: a.showPlan}

	a.mu.Lock()
	v.Plan, v.PlanNote = a.plan, a.planNote
	a.mu.Unlock()

	rows, err := collect(a.runner, a.lim, a.dir, a.selfCwd)
	if err != nil {
		v.Err = oneLine(err.Error())
		return v
	}
	v.Rows = rows
	return v
}

// refreshPlan fetches the plan gauge and stores it. capataz is an optional
// dependency here — the context gauges never need it — so a failure becomes a
// dim one-line note and the rest of the view carries on.
func (a *app) refreshPlan() {
	if !a.showPlan {
		return
	}
	out, err := a.runner.PlanUsage()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.plan = ""
		a.planNote = "plan usage unavailable (" + oneLine(err.Error()) + ")"
		return
	}
	a.plan, a.planNote = out, ""
}

// runOnce renders a single frame — used for -once, and whenever stdout or stdin
// isn't a terminal (piped output, CI, `usage-view | less`).
func (a *app) runOnce(out *os.File) error {
	a.refreshPlan() // synchronous: there is no later frame to fill it in
	width, height := termSize()
	if !isTTY(out) {
		height = 0 // no height budget: never truncate piped output
	}
	_, err := fmt.Fprintln(out, renderView(a.snapshot(false), width, height, a.color))
	return err
}

// runLive draws on a timer until the user quits, restoring the terminal on every
// exit path (including SIGINT/SIGTERM, so a killed popup can't leave the pane in
// cbreak mode with a hidden cursor).
func (a *app) runLive(out *os.File) error {
	st, err := enterCbreak()
	if err == nil {
		defer st.restore()
	}
	fmt.Fprint(out, altEnter+hideCursor)
	defer fmt.Fprint(out, showCursor+altLeave)

	quit := make(chan struct{})
	go readKeys(os.Stdin, quit)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	go a.refreshPlan()

	tick := time.NewTicker(a.interval)
	defer tick.Stop()
	planTick := time.NewTicker(a.planInterval)
	defer planTick.Stop()

	for {
		a.draw(out)
		select {
		case <-quit:
			return nil
		case <-sig:
			return nil
		case <-tick.C:
		case <-planTick.C:
			go a.refreshPlan()
		}
	}
}

// draw paints one frame in place. Rather than clearing the screen first (which
// flickers), it homes the cursor, rewrites each line with a clear-to-end-of-line,
// then clears whatever is left below — so a frame that shrinks leaves no debris.
func (a *app) draw(out *os.File) {
	width, height := termSize()
	frame := renderView(a.snapshot(true), width, height, a.color)
	lines := strings.Split(frame, "\n")

	var b strings.Builder
	b.WriteString(cursorHome)
	for i, line := range lines {
		b.WriteString(line)
		b.WriteString(clearEOL)
		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString(clearBelow)
	fmt.Fprint(out, b.String())
}

// readKeys closes quit on the first key that means "close this popup": q, Esc,
// Ctrl-C or Ctrl-D. Any other key is ignored (and, incidentally, forces no
// redraw — the timer owns the cadence).
func readKeys(in *os.File, quit chan<- struct{}) {
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if err != nil {
			close(quit)
			return
		}
		if n == 0 {
			continue
		}
		switch buf[0] {
		case 'q', 'Q', 27, 3, 4:
			close(quit)
			return
		}
	}
}

// oneLine flattens a multi-line error into something that fits a single row.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// selfBin is the capataz binary to shell out to: CAPATAZ_BIN if set, else the
// running executable (this command is itself a capataz subcommand).
func selfBin() string {
	if b := os.Getenv("CAPATAZ_BIN"); b != "" {
		return b
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "capataz"
}
