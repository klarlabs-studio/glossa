// Package httpapi is the Audit context's HTTP edge (RFC 0006 §6, §8):
// its operations of the generated /v1 strict server: the trail's
// entries, its export jobs and their two files (§6.2, trail.go), and the
// import of v0.3's history (§7.2).
// The composition root embeds API next to the other contexts' handlers;
// Identity's Guard has authenticated the caller and resolved the tenant
// before any of these run, and the use case checks the permission.
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// codeImportUnavailable answers an import on a server that does not run
// the importer. A code here is part of the contract: never rename it.
const codeImportUnavailable problem.Code = "audit_import_unavailable"

// Bounds of one import call, as api/openapi.yaml states them.
const (
	maxEntries       = 1000
	maxV0IDLen       = 64
	maxActorLen      = 128
	maxUnresolvedLen = 200
	maxRestoreLen    = 255
)

var (
	sha256Hex    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	actionFormat = regexp.MustCompile(`^v0\.[a-z][a-z0-9_.]{0,62}$`)
)

// API serves Audit's operations.
type API struct {
	importer app.V0HistoryImporter
	reads    Reads
	exports  Exports
}

// Option adds a part of the API.
type Option func(*API)

// WithReads serves the trail's entries.
func WithReads(r Reads) Option { return func(a *API) { a.reads = r } }

// WithExports serves the export jobs; without it they answer
// `audit_export_unavailable` (503).
func WithExports(e Exports) Option { return func(a *API) { a.exports = e } }

// New returns the API. importer records v0.3's history; nil makes the
// import answer `audit_import_unavailable` (503) — a server that does
// not run it says so rather than pretending to have recorded anything.
func New(importer app.V0HistoryImporter, opts ...Option) *API {
	a := &API{importer: importer}
	for _, o := range opts {
		o(a)
	}
	return a
}

// ImportV0History records a batch of v0.3's audit_log as imported audit
// entries of the project in the path. The body carries no text: a
// translation's before and after are SHA-256 digests, and this handler
// refuses anything in their place that is not one, so a digest field can
// never smuggle text into the trail (RFC 0006 §6.1).
func (a *API) ImportV0History(ctx context.Context, req apiv1.ImportV0HistoryRequestObject) (apiv1.ImportV0HistoryResponseObject, error) {
	project, err := uuid.Parse(req.Project)
	if err != nil || project == uuid.Nil {
		return nil, problem.New(http.StatusNotFound, problem.CodeNotFound, "no such project")
	}
	if a.importer == nil {
		return nil, problem.New(http.StatusServiceUnavailable, codeImportUnavailable,
			"this server does not import v0.3 history; nothing was recorded")
	}
	if req.Body == nil {
		return nil, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, "the body is a v0.3 history batch")
	}
	in, err := historyImport(project, *req.Body)
	if err != nil {
		return nil, err
	}
	r, err := a.importer.ImportV0History(ctx, in)
	if err != nil {
		return nil, mapError(err) // authorization and other failures: Identity's error writer
	}
	return apiv1.ImportV0History200JSONResponse{Recorded: r.Recorded, Existing: r.Existing}, nil
}

// historyImport checks a batch against the contract and turns it into
// the use case's shape, every row in project.
func historyImport(project uuid.UUID, body apiv1.V0HistoryImport) (app.V0HistoryImport, error) {
	var bad []problem.FieldError
	field := func(ptr, msg string) { bad = append(bad, problem.FieldError{Pointer: ptr, Detail: msg}) }
	if body.Restore == "" || len(body.Restore) > maxRestoreLen {
		field("/restore", fmt.Sprintf("the restore's name, 1–%d characters", maxRestoreLen))
	}
	if !sha256Hex.MatchString(body.RestoreSha256) {
		field("/restore_sha256", "a SHA-256 digest: 64 lower-case hex characters")
	}
	if n := len(body.Entries); n == 0 || n > maxEntries {
		field("/entries", fmt.Sprintf("1–%d rows per call", maxEntries))
	}
	out := app.V0HistoryImport{Restore: body.Restore, RestoreSHA256: body.RestoreSha256,
		Entries: make([]app.V0HistoryEntry, 0, len(body.Entries))}
	for i, e := range body.Entries {
		at := fmt.Sprintf("/entries/%d", i)
		row, problems := historyEntry(project, e)
		for _, p := range problems {
			field(at+p.Pointer, p.Detail)
		}
		out.Entries = append(out.Entries, row)
	}
	if len(bad) > 0 {
		return app.V0HistoryImport{}, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest,
			"the v0.3 history batch is not valid").WithErrors(bad...)
	}
	return out, nil
}

func historyEntry(project uuid.UUID, e apiv1.V0HistoryEntry) (app.V0HistoryEntry, []problem.FieldError) {
	var bad []problem.FieldError
	field := func(ptr, msg string) { bad = append(bad, problem.FieldError{Pointer: ptr, Detail: msg}) }
	if e.V0Id == "" || len(e.V0Id) > maxV0IDLen {
		field("/v0_id", fmt.Sprintf("v0.3's audit_log id, 1–%d characters", maxV0IDLen))
	}
	if !actionFormat.MatchString(e.Action) {
		field("/action", "a v0.3 action such as v0.translation.changed")
	}
	if !strings.HasPrefix(e.Actor, "v0:") || len(e.Actor) <= len("v0:") || len(e.Actor) > maxActorLen || hasControl(e.Actor) {
		field("/actor", "a v0.3 actor: v0:<id>, v0:ai:<label>, v0:system:<label> or v0:unknown")
	}
	if e.OccurredAt.IsZero() {
		field("/occurred_at", "when v0.3 recorded the change")
	}
	row := app.V0HistoryEntry{
		V0ID: e.V0Id, Action: e.Action, Actor: e.Actor, OccurredAt: e.OccurredAt.UTC(), Project: project,
	}
	for _, d := range []struct {
		ptr string
		v   *string
		to  *string
	}{{"/before_sha256", e.BeforeSha256, &row.BeforeSHA256}, {"/after_sha256", e.AfterSha256, &row.AfterSHA256}} {
		if d.v == nil {
			continue
		}
		if !sha256Hex.MatchString(*d.v) {
			// Never echo the value: it may be the text it should have
			// been a digest of.
			field(d.ptr, "a SHA-256 digest of the text, 64 lower-case hex characters — never the text itself")
			continue
		}
		*d.to = *d.v
	}
	if e.Key != nil {
		row.Key = *e.Key
	}
	if e.Locale != nil {
		row.Locale = *e.Locale
	}
	if e.Unresolved != nil {
		if len(*e.Unresolved) > maxUnresolvedLen || hasControl(*e.Unresolved) {
			field("/unresolved", fmt.Sprintf("why the row names no translation, at most %d characters", maxUnresolvedLen))
		}
		row.Unresolved = *e.Unresolved
	}
	if (row.Key == "") != (row.Locale == "") {
		field("/key", "a translation is named by key and locale together, or not at all")
	}
	return row, bad
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}
