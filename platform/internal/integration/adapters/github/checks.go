package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// GitHub's check-run limits.
const (
	// MaxAnnotationsPerRequest is GitHub's cap per create/update request.
	MaxAnnotationsPerRequest = 50
	// maxSummary is the output summary's limit in characters.
	maxSummary = 65535
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type checkRunJSON struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	HeadSHA    string  `json:"head_sha"`
	ExternalID string  `json:"external_id"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

func (j checkRunJSON) domain() app.CheckRun {
	out := app.CheckRun{ID: j.ID, Name: j.Name, HeadSHA: j.HeadSHA, ExternalID: j.ExternalID, Status: j.Status}
	if j.Conclusion != nil {
		out.Conclusion = *j.Conclusion
	}
	return out
}

func repoPath(repo int64) string { return "/repositories/" + strconv.FormatInt(repo, 10) }

func invalid(op, format string, args ...any) error {
	return &APIError{Op: op, Kind: app.ErrGitHubRejected, Message: fmt.Sprintf(format, args...)}
}

// EnsureCheckRun implements app.GitHub.
func (c *Client) EnsureCheckRun(ctx context.Context, t app.GitHubTarget, key app.CheckRunKey, externalID string, storedID int64) (app.CheckRun, error) {
	const op = "checks.ensure"
	if !shaPattern.MatchString(key.HeadSHA) || strings.TrimSpace(key.Name) == "" || t.RepositoryID <= 0 {
		return app.CheckRun{}, invalid(op, "a check run needs a repository, a 40-hex head SHA and a name")
	}
	if storedID != 0 {
		return app.CheckRun{ID: storedID, Name: key.Name, HeadSHA: key.HeadSHA, ExternalID: externalID}, nil
	}
	if run, ok, err := c.findCheckRun(ctx, t, key); err != nil || ok {
		return run, err
	}
	var out checkRunJSON
	err := c.do(ctx, request{
		op: "checks.create", tenant: t.TenantID, installation: t.InstallationID,
		method: http.MethodPost, path: repoPath(t.RepositoryID) + "/check-runs",
		body: map[string]any{"name": key.Name, "head_sha": key.HeadSHA, "external_id": externalID, "status": app.CheckQueued},
		out:  &out,
	})
	if err != nil {
		return app.CheckRun{}, err
	}
	c.log.InfoContext(ctx, "github check run created", "installation_id", t.InstallationID,
		"repository_id", t.RepositoryID, "check_run_id", out.ID)
	return out.domain(), nil
}

// findCheckRun looks for the App's newest check run named key.Name on the
// head SHA.
func (c *Client) findCheckRun(ctx context.Context, t app.GitHubTarget, key app.CheckRunKey) (app.CheckRun, bool, error) {
	var out struct {
		CheckRuns []checkRunJSON `json:"check_runs"`
	}
	err := c.do(ctx, request{
		op: "checks.list", tenant: t.TenantID, installation: t.InstallationID,
		method: http.MethodGet, path: repoPath(t.RepositoryID) + "/commits/" + key.HeadSHA + "/check-runs",
		query: url.Values{
			"check_name": {key.Name},
			"app_id":     {strconv.FormatInt(c.cfg.AppID, 10)},
			"filter":     {"latest"},
			"per_page":   {"100"},
		},
		out: &out, idempotent: true,
	})
	if err != nil {
		return app.CheckRun{}, false, err
	}
	for _, r := range out.CheckRuns {
		if r.Name == key.Name {
			return r.domain(), true, nil
		}
	}
	return app.CheckRun{}, false, nil
}

// UpdateCheckRun implements app.GitHub. Annotations go in batches of
// MaxAnnotationsPerRequest, one PATCH each; the status and conclusion
// ride on the last, so the run completes only once all are attached.
func (c *Client) UpdateCheckRun(ctx context.Context, t app.GitHubTarget, checkRunID int64, u app.CheckRunUpdate) error {
	const op = "checks.update"
	if err := validateUpdate(u); err != nil {
		return invalid(op, "%v", err)
	}
	annotations := make([]map[string]any, 0, len(u.Annotations))
	for _, a := range u.Annotations {
		annotations = append(annotations, annotationJSON(a))
	}
	batches := chunk(annotations, MaxAnnotationsPerRequest)
	for i, batch := range batches {
		body := map[string]any{}
		if u.Title != "" {
			body["output"] = map[string]any{"title": u.Title, "summary": truncate(u.Summary, maxSummary), "annotations": batch}
		}
		if i == len(batches)-1 {
			if u.Status != "" {
				body["status"] = u.Status
			}
			if u.Conclusion != "" {
				body["conclusion"] = u.Conclusion
				body["completed_at"] = c.opts.Now().UTC().Format("2006-01-02T15:04:05Z")
			}
		}
		err := c.do(ctx, request{
			op: op, tenant: t.TenantID, installation: t.InstallationID,
			method: http.MethodPatch, path: repoPath(t.RepositoryID) + "/check-runs/" + strconv.FormatInt(checkRunID, 10),
			body: body,
			// A repeated PATCH of status and output is harmless; a
			// repeated annotation batch is appended twice, which beats a
			// check that never completes.
			idempotent: true,
		})
		if err != nil {
			return err
		}
	}
	c.log.InfoContext(ctx, "github check run updated", "installation_id", t.InstallationID,
		"repository_id", t.RepositoryID, "check_run_id", checkRunID, "status", u.Status,
		"conclusion", u.Conclusion, "annotations", len(u.Annotations), "requests", len(batches))
	return nil
}

func validateUpdate(u app.CheckRunUpdate) error {
	switch u.Status {
	case "", app.CheckQueued, app.CheckInProgress:
		if u.Conclusion != "" {
			return fmt.Errorf("a conclusion needs status %s", app.CheckCompleted)
		}
	case app.CheckCompleted:
		switch u.Conclusion {
		case app.ConclusionSuccess, app.ConclusionFailure, app.ConclusionNeutral:
		default:
			return fmt.Errorf("conclusion %q is not success, failure or neutral", u.Conclusion)
		}
	default:
		return fmt.Errorf("unknown status %q", u.Status)
	}
	if u.Status == "" && u.Title == "" {
		return fmt.Errorf("nothing to update")
	}
	if (u.Title == "") != (u.Summary == "") {
		return fmt.Errorf("an output needs both a title and a summary")
	}
	if len(u.Annotations) > 0 && u.Title == "" {
		return fmt.Errorf("annotations need an output title and summary")
	}
	for _, a := range u.Annotations {
		if a.Path == "" || a.StartLine <= 0 || a.Message == "" {
			return fmt.Errorf("an annotation needs a path, a start line and a message")
		}
		switch a.Level {
		case "", "notice", "warning", "failure":
		default:
			return fmt.Errorf("annotation level %q is not notice, warning or failure", a.Level)
		}
	}
	return nil
}

func annotationJSON(a app.CheckAnnotation) map[string]any {
	end := a.EndLine
	if end < a.StartLine {
		end = a.StartLine
	}
	level := a.Level
	if level == "" {
		level = "warning"
	}
	out := map[string]any{
		"path": a.Path, "start_line": a.StartLine, "end_line": end,
		"annotation_level": level, "message": a.Message,
	}
	if a.Title != "" {
		out["title"] = a.Title
	}
	return out
}

// chunk splits items into batches of at most n, with at least one
// (possibly empty) batch.
func chunk[T any](items []T, n int) [][]T {
	if len(items) == 0 {
		return [][]T{{}}
	}
	var out [][]T
	for len(items) > n {
		out = append(out, items[:n])
		items = items[n:]
	}
	return append(out, items)
}

// truncate cuts s to at most n characters (runes), marking the cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	const mark = "\n\n… (truncated)"
	return string(r[:n-len([]rune(mark))]) + mark
}
