package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// ErrNoSnapshot means this deployment cannot read a project as the
// layers read it, so it cannot run a check on the server. It is a
// wiring fact, not a caller's mistake: a server without the snapshot
// port does not offer the check at all.
var ErrNoSnapshot = errors.New("quality: this deployment cannot read a project snapshot")

// ProjectSnapshot is a project as a check sees it: the catalog the
// layers grade, and the policy that grades them.
type ProjectSnapshot struct {
	Project *layers.Project
	Policy  checkpolicy.Policy
}

// Snapshot reads a project as the layers read it. It is Catalog's and
// Localization's data, reached through their application services (the
// adapter in quality/adapters/snapshot), so a check on the server sees
// exactly what a reader of the API would see and nothing more.
type Snapshot interface {
	// Snapshot returns the project and its policy, ErrProjectNotFound
	// for a project this caller cannot read.
	Snapshot(ctx context.Context, project uuid.UUID) (ProjectSnapshot, error)
}

// WithSnapshot lets the service run a check over a stored project.
// Without one, RunCheck answers ErrNoSnapshot.
func WithSnapshot(s Snapshot) Option {
	return func(svc *Service) {
		if s != nil {
			svc.snapshot = s
		}
	}
}

// CheckRequest narrows a check.
type CheckRequest struct {
	// Environment grades against the policy's block for it; "" is a
	// branch check, in no environment at all.
	Environment string
	// Layers are the deterministic layers to compute, by name. Empty
	// runs every one the policy leaves on; an unknown name is
	// ErrInvalidQuery, because silently checking less than was asked
	// for is how a check loses a layer.
	Layers []string
}

// RunCheck runs the deterministic layers over the project as it stands
// and returns the report — the same layers, the same policy and the
// same conclusion `glossa check` and the pull-request check reach,
// because there is one implementation of each (RFC 0005 §2.2).
//
// It stores nothing. Recording a run is RecordCheckRun's job and needs
// `catalog.write`: a verdict about a commit is history worth keeping,
// while "what is wrong right now" is a question, and a reader with
// `catalog.read` may ask it. That is what lets an MCP session with a
// read-only token run a check (RFC 0005 §7.3, §12.6).
func (s *Service) RunCheck(ctx context.Context, project uuid.UUID, in CheckRequest) (rep Report, err error) {
	if err := s.read(ctx, project); err != nil {
		return Report{}, err
	}
	if s.snapshot == nil {
		return Report{}, ErrNoSnapshot
	}
	checkers, err := checkersFor(in.Layers)
	if err != nil {
		return Report{}, err
	}
	ctx, end := s.span(ctx, "quality.run_check",
		attribute.String("glossa.project_id", project.String()),
		attribute.String("glossa.quality.environment", in.Environment))
	defer end(&err)

	snap, err := s.snapshot.Snapshot(ctx, project)
	if err != nil {
		return Report{}, err
	}
	if snap.Project == nil {
		return Report{}, ErrProjectNotFound
	}
	rep = RunIn(snap.Project, snap.Policy, in.Environment, checkers...)
	// The server is the one caller that has somewhere to put these: a
	// `glossa check` in a terminal measures the same thing and prints
	// nothing (RFC 0005 §11).
	for _, t := range rep.Took {
		s.metrics.LayerChecked(t.Layer, t.For)
	}
	s.metrics.PolicyVersionRead(project, snap.Policy.Version)
	return rep, nil
}

// checkersFor selects the deterministic layers by name.
func checkersFor(names []string) ([]layers.Checker, error) {
	all := layers.Default()
	if len(names) == 0 {
		return all, nil
	}
	out := make([]layers.Checker, 0, len(names))
	for _, c := range all {
		if slices.Contains(names, string(c.Layer())) {
			out = append(out, c)
		}
	}
	if len(out) != len(unique(names)) {
		return nil, fmt.Errorf("%w: a layer named is not one this check computes", ErrInvalidQuery)
	}
	return out, nil
}

func unique(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// LayerNames renders a report's layers as strings, for an adapter that
// answers in names rather than in domain types.
func LayerNames(ls []domain.Layer) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = string(l)
	}
	return out
}
