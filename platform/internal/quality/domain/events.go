package domain

// Quality's domain events (platform/README.md, "Domain events"). Their
// names and payloads are a published contract: a breaking change is a
// new ".v2" type, never an edit.
const (
	AggregateCheckRun = "check_run"

	// EventCheckRunRecorded: a check run and its findings were stored.
	// It is what tells the rest of the platform that a commit has been
	// graded — the Glossa pull-request check renders the run CI
	// recorded (RFC 0005 §12.3), and without it a pull request would sit
	// waiting on a verdict that had already been computed.
	EventCheckRunRecorded = "quality.check_run.recorded"
)

// CheckRunRecorded is the payload of quality.check_run.recorded: which
// run, of what, and what it concluded.
//
// It carries no findings. A subscriber that wants them reads the run,
// so a late, duplicated or reordered delivery cannot make anyone act on
// a stale copy of what was found.
type CheckRunRecorded struct {
	RunID     string `json:"run_id"`
	ProjectID string `json:"project_id"`
	// Ref is the branch or environment that was checked, and Commit the
	// commit it graded, where there is one.
	Ref        string `json:"ref"`
	Commit     string `json:"commit,omitempty"`
	Trigger    string `json:"trigger"`
	Conclusion string `json:"conclusion"`
}

// CheckRunRecordedOf builds the payload for r.
func CheckRunRecordedOf(r CheckRun) CheckRunRecorded {
	return CheckRunRecorded{
		RunID: r.ID.String(), ProjectID: r.Project.String(), Ref: r.Ref, Commit: r.Commit,
		Trigger: string(r.Trigger), Conclusion: string(r.Conclusion),
	}
}
