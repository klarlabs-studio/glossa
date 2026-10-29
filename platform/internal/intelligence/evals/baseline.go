package evals

import (
	_ "embed"
	"encoding/json"
)

//go:embed testdata/baseline.json
var baselineJSON []byte

// CommittedBaseline returns the tracked metrics committed with this
// build (testdata/baseline.json), which the API serves read-only.
func CommittedBaseline() (Baseline, error) {
	var b Baseline
	err := json.Unmarshal(baselineJSON, &b)
	return b, err
}
