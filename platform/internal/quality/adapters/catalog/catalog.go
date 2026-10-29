// Package catalog adapts Catalog's application service to Quality's
// Catalog port: that a project exists, the check-policy document it
// stores, and the branches of it that are open pull requests
// (RFC 0002 §4, contexts reach each other through application ports).
// Every call is an ordinary authorized Catalog use case, so Quality
// learns nothing its caller couldn't read through the API.
package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

// maxPreviewBranches bounds the open branches the impact preview asks
// about. The preview itself covers at most a few dozen refs, so a page
// this size answers for every one of them and a project with a thousand
// stale branches cannot turn a dry run into a scan.
const maxPreviewBranches = 200

// Port implements app.Catalog on Catalog's service.
type Port struct{ svc *catalogapp.Service }

// New returns the port.
func New(svc *catalogapp.Service) *Port { return &Port{svc: svc} }

var _ app.Catalog = (*Port)(nil)

// Project implements app.Catalog.
func (p *Port) Project(ctx context.Context, project uuid.UUID) error {
	_, err := p.svc.GetProject(ctx, catalogdomain.ProjectID(project))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}

// keyBatch bounds one key lookup. A run may name ten thousand
// findings; the catalog is asked about their distinct keys in batches
// this size, so one query's parameter list stays bounded whatever a run
// carries.
const keyBatch = 500

// MessageIDs implements app.Catalog with Catalog's key lookup, in
// batches. Messages of every state resolve: a finding about an obsolete
// message still names it, and the fingerprint is the same one every
// other surface computes for it.
func (p *Port) MessageIDs(ctx context.Context, project uuid.UUID, keys []string) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID, len(keys))
	for start := 0; start < len(keys); start += keyBatch {
		found, err := p.svc.MessagesByKeys(ctx, catalogdomain.ProjectID(project), keys[start:min(start+keyBatch, len(keys))])
		if errors.Is(err, catalogapp.ErrNotFound) {
			return nil, app.ErrProjectNotFound
		}
		if err != nil {
			return nil, err
		}
		for k, m := range found {
			out[k] = m.ID.UUID()
		}
	}
	return out, nil
}

// CheckPolicy implements app.Catalog: the document the project stores,
// with the project row's version, which a save has to still match.
func (p *Port) CheckPolicy(ctx context.Context, project uuid.UUID) (app.StoredPolicy, error) {
	pr, err := p.svc.GetProject(ctx, catalogdomain.ProjectID(project))
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.StoredPolicy{}, app.ErrProjectNotFound
	}
	if err != nil {
		return app.StoredPolicy{}, err
	}
	return app.StoredPolicy{Policy: pr.Settings.Policy(), ProjectVersion: pr.Version}, nil
}

// SaveCheckPolicy implements app.Catalog by writing the document into
// the project's settings, which is where every reader of the policy
// already looks. A write that lost the race on the project row is
// refused rather than allowed to drop what the winner said.
func (p *Port) SaveCheckPolicy(ctx context.Context, project uuid.UUID, ifMatch int, doc checkpolicy.Policy) error {
	id := catalogdomain.ProjectID(project)
	// A settings write is the whole block, so the project's other
	// settings are carried over unchanged: Quality replaces the check
	// policy and nothing else. The version the read saw still guards the
	// write, so a project that changed in between is a conflict rather
	// than a silent overwrite of whatever else moved.
	current, err := p.svc.GetProject(ctx, id)
	if errors.Is(err, catalogapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	if err != nil {
		return err
	}
	settings := current.Settings
	settings.CheckPolicy = &doc
	_, err = p.svc.UpdateProject(ctx, id, ifMatch, catalogdomain.ProjectChange{Settings: &settings})
	switch {
	case errors.Is(err, catalogapp.ErrNotFound):
		return app.ErrProjectNotFound
	case errors.Is(err, catalogapp.ErrPreconditionFailed):
		return app.ErrPolicyConflict
	}
	return err
}

// OpenPullRequests implements app.Catalog with the branch overlay: the
// open branches that carry a pull request number (RFC 0004 §4.1). A
// branch with no number is not in the map — it is somebody's local
// branch, and nobody wakes up to it going red.
func (p *Port) OpenPullRequests(ctx context.Context, project uuid.UUID) (map[string]int, error) {
	bs, _, err := p.svc.ListBranches(ctx, catalogdomain.ProjectID(project),
		catalogapp.BranchFilter{State: catalogdomain.BranchOpen}, pagination.Page{Size: maxPreviewBranches})
	if errors.Is(err, catalogapp.ErrNotFound) {
		return nil, app.ErrProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(bs))
	for _, b := range bs {
		if b.PR != nil && *b.PR > 0 {
			out[string(b.Name)] = *b.PR
		}
	}
	return out, nil
}
