package drain

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// stubHerdr writes a herdr stand-in that prints stdout and exits with code.
func stubHerdr(t *testing.T, stdout string, code int) *realRunner {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\nprintf '%s' '" + stdout + "'\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &realRunner{herdrBin: bin}
}

func TestAgentWait_ErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name, stdout string
		code         int
		want         string
		wantErr      bool
	}{
		{"timeout", `{"error":{"code":"timeout","message":"timed out waiting for agent status"},"id":"cli:agent:wait"}`, 1, "timeout", false},
		{"agent gone", `{"error":{"code":"agent_not_found","message":"no such agent"},"id":"cli:agent:wait"}`, 1, statusGone, false},
		{"other error", `{"error":{"code":"boom","message":"bad"}}`, 1, "error", true},
		{"non-json failure", `oops`, 2, "error", true},
		{"done", `{"result":{"status":"done"}}`, 0, "done", false},
		{"idle folds to done", `{"result":{"state":"idle"}}`, 0, "done", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stubHerdr(t, tc.stdout, tc.code).AgentWait("a", 1000)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("AgentWait = %q, %v; want %q, err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
