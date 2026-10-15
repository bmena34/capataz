package drain

import (
	"encoding/json"
	"fmt"
)

// WaveIssue mirrors the per-issue entry emitted by `capataz issue waves --json`.
// Fields match the JSON schema produced by internal/issues/cli.go wavesJSON.
type WaveIssue struct {
	Number  string `json:"number"`
	Path    string `json:"path"`
	Feature string `json:"feature"`
	Status  string `json:"status"`
	Branch  string `json:"branch"`
}

// WavesOutput is the top-level envelope from `capataz issue waves --json`.
type WavesOutput struct {
	Feature string        `json:"feature"`
	Waves   [][]WaveIssue `json:"waves"`
}

// parseWaves decodes the JSON produced by `capataz issue waves --json`.
// Returns an error if the JSON is malformed or missing required fields.
func parseWaves(data []byte) (WavesOutput, error) {
	var out WavesOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return WavesOutput{}, fmt.Errorf("parseWaves: %w", err)
	}
	if out.Feature == "" {
		return WavesOutput{}, fmt.Errorf("parseWaves: missing feature field")
	}
	return out, nil
}

// dispatchable returns the issues in the first wave that are dispatchable
// (have a path and branch), up to the concurrency cap. If cap <= 0 it returns
// the full first wave.
func dispatchable(waves [][]WaveIssue, cap int) []WaveIssue {
	if len(waves) == 0 {
		return nil
	}
	wave := waves[0]
	var out []WaveIssue
	for _, iss := range wave {
		if iss.Path == "" || iss.Branch == "" {
			continue
		}
		out = append(out, iss)
		if cap > 0 && len(out) >= cap {
			break
		}
	}
	return out
}
