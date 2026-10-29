//go:build system

package m4_test

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// The release gate (RFC 0005 §12.5): publishing to `production` with
// `fr` incomplete fails with `policy_not_met`; the forced publish
// succeeds and writes an audit entry with its reason.
//
// The environment's requirement is policy v3's, which lists `fr` among
// the locales production must ship complete — and the fixture is seeded
// with three French translations that were never written, so the
// requirement is not met.

// approveFor approves every translation a locale has, the way a
// reviewer would. A release ships what the environment's review
// requirement makes eligible, and nothing a token pushed is approved:
// `translations.review` is in no scope's permission set, because review
// is a human decision. So the three French translations that were never
// written are the only thing standing between this project and a
// production release, which is what §12.5 is about.
func (s *scenario) approveFor(locales ...string) {
	for _, locale := range locales {
		keys := make([]string, 0, len(s.repo.head[locale]))
		for k := range s.repo.head[locale] {
			keys = append(keys, k)
		}
		errs := parallel(keys, 8, func(key string) error {
			path := s.projectPath("/messages/" + key + "/translations/" + locale)
			h, err := s.owner.try(http.MethodGet, path, nil, http.StatusOK, nil)
			if err != nil {
				return err
			}
			_, err = s.owner.try(http.MethodPost, path+"/reviews",
				map[string]any{"state": "approved"}, http.StatusOK, nil, "If-Match", h.Get("ETag"))
			return err
		})
		if len(errs) > 0 {
			s.t.Logf("%d of %s's %d translations could not be approved (first: %v)", len(errs), locale, len(keys), errs[0])
		}
	}
}

func (s *scenario) releaseGate() {
	// Everything the production environment requires, approved — except
	// what nobody wrote.
	s.approveFor("en", "fr")

	// The environment exists before anything is published to it.
	var env struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if _, err := s.owner.try(http.MethodPost, s.projectPath("/environments"),
		map[string]any{"name": "production", "kind": "static"}, http.StatusCreated, &env); err != nil {
		// Some deployments have production already.
		for _, e := range list[struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}](s.owner, s.projectPath("/environments"), nil) {
			if e.Name == "production" {
				env.ID, env.Name = e.ID, e.Name
			}
		}
	}
	if env.Name != "production" {
		s.gap("12.5", "there is no `production` environment to publish to")
		return
	}

	_, err := s.owner.try(http.MethodPost, s.projectPath("/releases"),
		map[string]any{"environment": "production", "note": "the first release"}, http.StatusCreated, nil)
	var ae *apiError
	switch {
	case err == nil:
		s.gap("12.5", "publishing to production succeeded although French is incomplete")
	case !asAPIError(err, &ae):
		s.gap("12.5", "publishing to production failed with %v, want a problem document", err)
	case ae.status != http.StatusConflict || ae.code() != "policy_not_met":
		s.gap("12.5", "publishing to production was refused as HTTP %d `%s`, want 409 policy_not_met",
			ae.status, ae.code())
	default:
		s.releaseRows = append(s.releaseRows, step{
			What: "publish to `production` with `fr` incomplete",
			Then: fmt.Sprintf("409 `policy_not_met` — %s", detailOf(ae))})
	}

	// A force with no reason is refused; the exception has to be part
	// of the history.
	_, err = s.owner.try(http.MethodPost, s.projectPath("/releases"),
		map[string]any{"environment": "production", "force": true}, http.StatusCreated, nil)
	switch {
	case err == nil:
		s.gap("12.5", "a forced publish with no reason succeeded")
	case asAPIError(err, &ae) && ae.code() == "policy_not_met":
		// The precise shortfall, not a vague one: the two are answers to
		// different questions, and a caller told `policy_not_met` about
		// a forced publish has been told the override does not exist.
		s.gap("12.5", "a forced publish with no reason was refused as `policy_not_met`, not "+
			"`force_reason_required`: the gate ran before the force was read, so the request's `force` did "+
			"not reach `app.Service.Publish` (`release/adapters/httpapi.API.PublishRelease`)")
	case asAPIError(err, &ae) && ae.code() != "force_reason_required":
		s.gap("12.5", "a forced publish with no reason was refused as %q, want force_reason_required", ae.code())
	default:
		s.releaseRows = append(s.releaseRows, step{
			What: "force it with no reason", Then: "400 `force_reason_required`"})
	}

	const forceReason = "The French launch is Monday; the three missing strings are behind a flag until then."
	var release struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if _, err := s.owner.try(http.MethodPost, s.projectPath("/releases"), map[string]any{
		"environment": "production", "force": true, "force_reason": forceReason,
	}, http.StatusCreated, &release); err != nil {
		s.gap("12.5", "the forced publish failed: %v", err)
		return
	}
	s.releaseRows = append(s.releaseRows, step{
		What: "force it with a reason",
		Then: fmt.Sprintf("201 — release `%s`, version %d", short(release.ID), release.Version)})

	// The record: a forced deployment always carries its reason.
	deployments := list[struct {
		Number      int    `json:"number"`
		ReleaseID   string `json:"release_id"`
		Action      string `json:"action"`
		Author      string `json:"author"`
		Forced      bool   `json:"forced"`
		ForceReason string `json:"force_reason"`
	}](s.owner, s.projectPath("/environments/production/deployments"), nil)
	found := false
	for _, d := range deployments {
		if d.ReleaseID != release.ID {
			continue
		}
		found = true
		switch {
		case !d.Forced:
			s.gap("12.5", "the deployment is not marked forced")
		case d.ForceReason != forceReason:
			s.gap("12.5", "the deployment's reason is %q", d.ForceReason)
		default:
			s.releaseRows = append(s.releaseRows, step{
				What: "the record",
				Then: fmt.Sprintf("deployment #%d by `%s`, `forced: true`, with the reason it was forced for",
					d.Number, d.Author)})
		}
	}
	if !found {
		s.gap("12.5", "the forced publish wrote no deployment record (%d deployments)", len(deployments))
	}
}

func detailOf(e *apiError) string {
	var p struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal([]byte(e.body), &p)
	if p.Detail == "" {
		return e.body
	}
	return p.Detail
}
