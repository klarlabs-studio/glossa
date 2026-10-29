//go:build system

package m5_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// sweepUnit is the unit of each project the sweep's per-unit resources
// are about: in project B one of the vendor's assigned units that no
// other criterion touches, in project A its counterpart.
const sweepUnit = 19

// sweepQuery is the fixture's value for a required `q`: a word every
// source text of both projects holds.
const sweepQuery = "Entry"

// sweepSide is one project's share of the resources §12.2's sweep
// addresses: what it is called with inside the assignment (project B)
// or outside it (project A).
type sweepSide struct {
	project, prefix string
	application     string
	release         string
}

// sweepResources makes, in each project, one of every resource a GET of
// the contract addresses by an id the parent collection cannot hand the
// sweep — an import job, a Git connection, a branch, a capture, a check
// run, a linguistic job, an AI fill with its job and suggestion, and a
// release artifact — through the API as the owner, the way a person or
// CI would. Project B's go into s.sweepInside, project A's into
// s.sweepOutside, so every such operation is called inside the
// assignment and outside it. Anything the platform refuses to make is a
// note of §12.2, and its operations stay without a verdict in the
// coverage table, which counts against §12.2 rather than hiding.
func (s *scenario) sweepResources(sides [2]sweepSide) {
	s.sweepInside, s.sweepOutside = map[string]string{"q": sweepQuery}, map[string]string{"q": sweepQuery}
	installation := s.githubInstallation()
	for i, side := range sides {
		ids := s.sweepOutside
		if side.project == s.projectB {
			ids = s.sweepInside
		}
		unit := unitKey(side.prefix, sweepUnit)
		for _, r := range []struct {
			what string
			fn   func() error
		}{
			{"an import job", func() error { return s.sweepImportJob(side, ids) }},
			{"a Git connection", func() error { return s.sweepConnection(side, installation, i, ids) }},
			{"a branch", func() error { return s.sweepBranch(side, unit, ids) }},
			{"a capture", func() error { return s.sweepCapture(side, unit, ids) }},
			{"a check run", func() error { return s.sweepCheckRun(side, ids) }},
			{"a linguistic job", func() error { return s.sweepLinguisticJob(side, unit, ids) }},
			{"a release artifact", func() error { return s.sweepArtifact(side, ids) }},
			{"a rollout and a release request", func() error { return s.sweepReleaseFlow(side, ids) }},
		} {
			if err := r.fn(); err != nil {
				s.note("12.2", "The fixture could not make %s in project %s for the sweep: %v", r.what, strings.ToUpper(side.prefix), err)
			}
		}
	}
	// Tenant- and person-level resources: one id serves both sides.
	for _, r := range []struct {
		what string
		fn   func() (string, string, error)
	}{
		{"an audit export job", s.sweepAuditExportJob},
		{"a device authorization", s.sweepDeviceAuthorization},
	} {
		name, id, err := r.fn()
		if err != nil {
			s.note("12.2", "The fixture could not make %s for the sweep: %v", r.what, err)
			continue
		}
		s.sweepInside[name], s.sweepOutside[name] = id, id
	}
	// The AI fills last and together: their jobs run while the rest is
	// made, and both are waited for at once.
	if err := s.sweepAI(sides); err != nil {
		s.note("12.2", "The fixture could not make an AI fill, job and suggestion in both projects for the sweep: %v", err)
	}
}

func (s *scenario) sweepImportJob(side sweepSide, ids map[string]string) error {
	var job struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, s.tenantPath("/import-jobs"), map[string]any{
		"project_id": side.project, "format": "xliff", "mode": "dry_run", "file_name": "sweep.xliff",
	}, http.StatusCreated, &job); err != nil {
		return err
	}
	ids["import_job"] = job.ID
	return nil
}

// githubInstallation runs the GitHub App's install flow once, against
// the fake GitHub, and returns Glossa's installation id.
func (s *scenario) githubInstallation() string {
	var intent struct {
		State string `json:"state"`
	}
	if _, err := s.owner.try(http.MethodPost, s.tenantPath("/github/install-intents"), map[string]any{}, http.StatusCreated, &intent); err != nil {
		s.note("12.2", "The fixture could not start the GitHub App's install flow: %v", err)
		return ""
	}
	var installation struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, s.tenantPath("/github/installations"), map[string]any{
		"state": intent.State, "code": "code-m5", "installation_id": installationID,
	}, http.StatusCreated, &installation); err != nil {
		s.note("12.2", "The fixture could not complete the GitHub App's installation: %v", err)
		return ""
	}
	return installation.ID
}

// sweepConnection connects the one repository to the project, under a
// path of its own: one repository feeds both projects.
func (s *scenario) sweepConnection(side sweepSide, installation string, i int, ids map[string]string) error {
	if installation == "" {
		return fmt.Errorf("there is no GitHub installation")
	}
	var c struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, s.tenantPath("/github/connections"), map[string]any{
		"installation_id": installation, "repository_id": repositoryID, "project_id": side.project,
		"application_id": side.application, "default_branch": "main", "path": []string{"ledger", "portal"}[i],
	}, http.StatusCreated, &c); err != nil {
		return err
	}
	ids["connection"] = c.ID
	return nil
}

// sweepBranch pushes a branch that proposes new source for the unit.
func (s *scenario) sweepBranch(side sweepSide, unit string, ids map[string]string) error {
	var r struct {
		Branch struct {
			ID string `json:"id"`
		} `json:"branch"`
	}
	if _, err := s.owner.try(http.MethodPost, s.projectPathOf(side.project, "/branch-pushes"), map[string]any{
		"branch": "sweep/copy", "head_commit": sweepCommit,
		"items": []map[string]any{{"key": unit, "text": "Entry 19, proposed on a branch", "syntax": "mf2"}},
	}, http.StatusOK, &r); err != nil {
		return err
	}
	if r.Branch.ID == "" {
		return fmt.Errorf("the push answered no branch id")
	}
	ids["branch"] = r.Branch.ID
	return nil
}

const sweepCommit = "5eed000000000000000000000000000000000019"

// sweepCapture uploads one capture of the unit, the way `glossa capture
// --upload` does: a manifest and one PNG.
func (s *scenario) sweepCapture(side sweepSide, unit string, ids map[string]string) error {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: uint8(10 * x), A: 255})
		}
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		return err
	}
	sum := sha256.Sum256(pngBytes.Bytes())
	digest := hex.EncodeToString(sum[:])
	manifest, err := json.Marshal(map[string]any{
		"schema": "glossa.captures/v1", "application": "web", "commit": sweepCommit, "branch": "main",
		"tool": map[string]string{"name": "glossa", "version": "0.0.0"},
		"captures": []map[string]any{{
			"route": "/sweep", "url": "http://localhost/sweep", "viewport": map[string]int{"width": 4, "height": 4},
			"locale": "de", "image": map[string]any{"sha256": digest, "width": 4, "height": 4},
			"renders": []any{},
			"regions": []map[string]any{{"key": unit, "kind": "element", "visible": true,
				"box": map[string]int{"x": 0, "y": 0, "width": 2, "height": 2}}},
		}},
	})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, part := range []struct {
		name, contentType string
		data              []byte
	}{{"manifest", "application/json", manifest}, {digest, "image/png", pngBytes.Bytes()}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, part.name))
		h.Set("Content-Type", part.contentType)
		pw, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		if _, err := pw.Write(part.data); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	if _, _, err := s.owner.send(http.MethodPost, s.projectPathOf(side.project, "/captures"), &body,
		w.FormDataContentType(), http.StatusCreated, nil); err != nil {
		return err
	}
	var mc struct {
		Captures []struct {
			ID string `json:"id"`
		} `json:"captures"`
	}
	if _, err := s.owner.try(http.MethodGet, s.projectPathOf(side.project, "/messages/"+unit+"/captures"), nil, http.StatusOK, &mc); err != nil {
		return err
	}
	if len(mc.Captures) == 0 {
		return fmt.Errorf("the upload stored no capture of %s", unit)
	}
	ids["capture"] = mc.Captures[0].ID
	return nil
}

func (s *scenario) sweepCheckRun(side sweepSide, ids map[string]string) error {
	var run struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, s.projectPathOf(side.project, "/check-runs"), map[string]any{
		"ref": "main", "commit": sweepCommit, "layers": []string{"structure"},
	}, http.StatusCreated, &run); err != nil {
		return err
	}
	ids["check_run"] = run.ID
	return nil
}

// sweepLinguisticJob asks for a linguistic review of the unit. The
// routing policy routes no review task, so the job is created and
// fails `no_route` without calling anything: it only has to exist.
func (s *scenario) sweepLinguisticJob(side sweepSide, unit string, ids map[string]string) error {
	var job struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, s.projectPathOf(side.project, "/linguistic-jobs"), map[string]any{
		"ref": "main", "scope": map[string]any{"locales": []string{"de"}, "keys": []string{unit}},
	}, http.StatusCreated, &job, "Idempotency-Key", "m5-sweep-linguistic-"+side.prefix); err != nil {
		return err
	}
	ids["linguistic_job"] = job.ID
	return nil
}

var sha256Field = regexp.MustCompile(`"sha256"\s*:\s*"([0-9a-f]{64})"`)

// sweepArtifact names one artifact of the project's staging release:
// the release and the digest, which the release's manifest lists.
func (s *scenario) sweepArtifact(side sweepSide, ids map[string]string) error {
	if side.release == "" {
		return fmt.Errorf("there is no staging release")
	}
	status, body, err := s.owner.get(s.projectPathOf(side.project, "/releases/"+side.release+"/manifest?environment=staging"))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("the staging release's manifest answered %d", status)
	}
	m := sha256Field.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("the staging release's manifest lists no artifact")
	}
	ids["release"], ids["digest"] = side.release, string(m[1])
	return nil
}

// sweepReleaseFlow leaves, in the project's `production` (the
// environment the sweep addresses), one ended rollout and one withdrawn
// release request, and the environment as it found it otherwise: a
// release served, no rollout active, no approval required — so §12.3 and
// §12.4 start from what they would have without it. A production
// release is published, the staging release is rolled out and the
// rollout aborted, then an approval requirement holds a publish as a
// request, which is withdrawn before the requirement is switched off.
func (s *scenario) sweepReleaseFlow(side sweepSide, ids map[string]string) error {
	if side.release == "" {
		return fmt.Errorf("there is no staging release to roll out")
	}
	if _, err := s.publish(s.owner, side.project, "production", map[string]any{"note": "for the sweep"}, http.StatusCreated); err != nil {
		return fmt.Errorf("the production release: %w", err)
	}
	rollouts := s.rolloutsPath(side.project, "production")
	var ro struct {
		ID string `json:"id"`
	}
	if _, err := s.owner.try(http.MethodPost, rollouts, map[string]any{"release_id": side.release, "percent": 5},
		http.StatusCreated, &ro, "Idempotency-Key", "m5-sweep-rollout-"+side.prefix); err != nil {
		return fmt.Errorf("starting a rollout: %w", err)
	}
	if _, err := s.owner.try(http.MethodPost, rollouts+"/"+ro.ID+"/abort", nil, http.StatusOK, nil); err != nil {
		return fmt.Errorf("aborting the rollout: %w", err)
	}
	ids["rollout"] = ro.ID

	env := s.projectPathOf(side.project, "/environments/production")
	var e struct {
		Policy map[string]any `json:"policy"`
	}
	h, err := s.owner.try(http.MethodGet, env, nil, http.StatusOK, &e)
	if err != nil {
		return err
	}
	h, err = s.owner.try(http.MethodPatch, env, map[string]any{"policy": e.Policy, "approval": map[string]any{
		"n": 1, "from": map[string]any{"role": "reviewer"}, "distinct_from_requester": true,
	}}, http.StatusOK, nil, "If-Match", h.Get("ETag"))
	if err != nil {
		return fmt.Errorf("requiring approval: %w", err)
	}
	var held struct {
		RequestID string `json:"release_request_id"`
	}
	_, heldErr := s.owner.try(http.MethodPost, s.projectPathOf(side.project, "/releases"),
		map[string]any{"environment": "production", "note": "held for the sweep"}, http.StatusAccepted, &held)
	if heldErr == nil {
		if _, err := s.owner.try(http.MethodPost, s.releaseRequestsPath(side.project)+"/"+held.RequestID+"/withdrawal",
			map[string]any{"reason": "the sweep only reads it"}, http.StatusOK, nil); err != nil {
			heldErr = fmt.Errorf("withdrawing the request: %w", err)
		}
		ids["release_request"] = held.RequestID
	}
	// The requirement goes whatever happened above, so later criteria
	// find production as they expect it.
	if _, err := s.owner.try(http.MethodPatch, env, map[string]any{"policy": e.Policy, "clear_approval": true},
		http.StatusOK, nil, "If-Match", h.Get("ETag")); err != nil {
		return fmt.Errorf("switching the approval off again: %w", err)
	}
	if heldErr != nil {
		return fmt.Errorf("a held publish: %w", heldErr)
	}
	return nil
}

// sweepAI fills the unit in both projects with the fake provider and
// waits for each fill's job and suggestion.
func (s *scenario) sweepAI(sides [2]sweepSide) error {
	fills := map[string]string{}
	for _, side := range sides {
		var fill struct {
			ID string `json:"id"`
		}
		if _, err := s.owner.try(http.MethodPost, s.projectPathOf(side.project, "/ai-fills"), map[string]any{
			"locales": []string{"de"}, "keys": []string{unitKey(side.prefix, sweepUnit)}, "force": true,
		}, http.StatusCreated, &fill, "Idempotency-Key", "m5-sweep-fill-"+side.prefix); err != nil {
			return fmt.Errorf("project %s's fill: %w", strings.ToUpper(side.prefix), err)
		}
		fills[side.project] = fill.ID
	}
	deadline := time.Now().Add(60 * time.Second)
	for _, side := range sides {
		ids := s.sweepOutside
		if side.project == s.projectB {
			ids = s.sweepInside
		}
		ids["ai_fill"] = fills[side.project]
		for {
			job, suggestion, state := s.fillOutcome(side.project, fills[side.project])
			if suggestion != "" {
				ids["ai_job"], ids["ai_suggestion"] = job, suggestion
				break
			}
			if time.Now().After(deadline) {
				if job != "" {
					ids["ai_job"] = job
				}
				return fmt.Errorf("project %s's fill made no suggestion in 60s (job %q is %q; the fake provider refused %v)",
					strings.ToUpper(side.prefix), job, state, s.d.provider.refusedRequests())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return nil
}

// fillOutcome is the fill's job, its state, and the suggestion it made.
func (s *scenario) fillOutcome(project, fill string) (job, suggestion, state string) {
	var jobs struct {
		Items []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"items"`
	}
	if _, err := s.owner.try(http.MethodGet, s.tenantPath("/ai-jobs?"+url.Values{"fill": {fill}}.Encode()), nil, http.StatusOK, &jobs); err != nil || len(jobs.Items) == 0 {
		return "", "", ""
	}
	job, state = jobs.Items[0].ID, jobs.Items[0].State
	var suggestions struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if _, err := s.owner.try(http.MethodGet, s.tenantPath("/ai-suggestions?"+url.Values{"project": {project}, "job": {job}}.Encode()),
		nil, http.StatusOK, &suggestions); err == nil && len(suggestions.Items) > 0 {
		suggestion = suggestions.Items[0].ID
	}
	return job, suggestion, state
}

func (p *fakeProvider) refusedRequests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.refused...)
}

// sweepAuditExportJob starts an export of the last hour as the owner, so
// the sweep can address a job and its two files.
func (s *scenario) sweepAuditExportJob() (string, string, error) {
	var job struct {
		ID string `json:"id"`
	}
	_, err := s.owner.try(http.MethodPost, s.auditExportJobsPath(), map[string]any{
		"from": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339),
	}, http.StatusCreated, &job)
	return "audit_export_job", job.ID, err
}

// sweepDeviceAuthorization starts a device sign-in the way the CLI does,
// unauthenticated and outside the harness's call log (no one acts, so
// the trail has nothing to record), for its user code.
func (s *scenario) sweepDeviceAuthorization() (string, string, error) {
	body := strings.NewReader(`{"client_name":"m5 sweep"}`)
	resp, err := http.Post(s.owner.base+"/v1/auth/device-authorizations", "application/json", body)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var d struct {
		UserCode string `json:"user_code"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("POST /v1/auth/device-authorizations answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", "", err
	}
	return "user_code", d.UserCode, nil
}
