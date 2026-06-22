// Package logging provides a small, process-safe file logger shared by every
// capataz component (daemon, hook, tui, install). All processes append to
// ~/.capataz/capataz.log; each line is tagged with a timestamp, level, and the
// component that wrote it. Writing to a file (never stdout) keeps it safe for
// the alt-screen TUI.
package logging

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bmena34/capataz/internal/paths"
)

var (
	mu        sync.Mutex
	component = "app"
	debug     = os.Getenv("CAPATAZ_DEBUG") != ""
)

// SetComponent tags subsequent log lines with the given component name.
func SetComponent(c string) { component = c }

// SetDebug toggles whether Debugf lines are written.
func SetDebug(on bool) { debug = on }

func write(level, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(paths.AppLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	// O_APPEND makes each small write atomic across processes; one line per call.
	fmt.Fprintf(f, "%s %-5s [%s] %s\n", ts, level, component, fmt.Sprintf(format, args...))
}

// Infof logs an informational line.
func Infof(format string, args ...any) { write("INFO", format, args...) }

// Errorf logs an error line.
func Errorf(format string, args ...any) { write("ERROR", format, args...) }

// Debugf logs a line only when debug logging is enabled (CAPATAZ_DEBUG set).
func Debugf(format string, args ...any) {
	if debug {
		write("DEBUG", format, args...)
	}
}
