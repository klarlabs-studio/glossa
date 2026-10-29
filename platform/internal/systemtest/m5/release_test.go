//go:build system

package m5_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ── shared: releases and the edge ────────────────────────────────────

// edgeManifest is the part of a manifest the criteria read, from the
// edge process over HTTP — never from the database.
type edgeManifest struct {
	Release struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	} `json:"release"`
	Rollout *struct {
		ID        string `json:"id"`
		Percent   int    `json:"percent"`
		Salt      string `json:"salt"`
		Candidate struct {
			Release struct {
				ID string `json:"id"`
			} `json:"release"`
		} `json:"candidate"`
	} `json:"rollout"`
	raw []byte
}

// edgeRow is one observation of what the edge served, for the report.
type edgeRow struct {
	When, Served string
	OK           bool
}

func (s *scenario) deliveryKey(project, name string) string {
	var key struct {
		Key string `json:"key"`
	}
	s.owner.do(http.MethodPost, s.projectPathOf(project, "/delivery-keys"), map[string]any{"name": name}, http.StatusCreated, &key)
	return key.Key
}

func (s *scenario) fetchManifest(key, env string) (edgeManifest, error) {
	resp, err := http.Get(s.d.edgeURL + "/v1/" + key + "/" + env + "/manifest.json")
	if err != nil {
		return edgeManifest{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return edgeManifest{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return edgeManifest{}, fmt.Errorf("the edge answered %d: %s", resp.StatusCode, raw)
	}
	var m edgeManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return edgeManifest{}, err
	}
	m.raw = raw
	return m, nil
}

// edgeServes waits for the edge to serve release want, and reports what
// it served last.
func (s *scenario) edgeServes(key, env, want string, wait time.Duration) (bool, string) {
	return softly(wait, func() (bool, string) {
		m, err := s.fetchManifest(key, env)
		if err != nil {
			return false, err.Error()
		}
		return m.Release.ID == want, "release " + short(m.Release.ID)
	})
}

type release struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// publish publishes the project to env as the given client.
func (s *scenario) publish(as *client, project, env string, body map[string]any, want int) (release, error) {
	if body == nil {
		body = map[string]any{}
	}
	body["environment"] = env
	var r release
	_, err := as.try(http.MethodPost, s.projectPathOf(project, "/releases"), body, want, &r)
	return r, err
}

// touch revises one approved German translation, so the next release
// differs from the last.
func (s *scenario) touch(project, key, text string) {
	if err := s.owner.putTranslation(s.projectPathOf(project, "/messages/"+key+"/translations/de"),
		map[string]any{"text": text, "syntax": "mf2", "state": "approved", "origin": "human"}); err != nil {
		fatalf("revise %s's German: %v", key, err)
	}
}

// ── §12.3 Release approvals ──────────────────────────────────────────

func (s *scenario) releaseApprovals() {
	const id = "12.3"
	key := s.deliveryKey(s.projectB, "m5-portal")
	stable, err := s.publish(s.owner, s.projectB, "production", map[string]any{"note": "before approvals"}, http.StatusCreated)
	if err != nil {
		s.gap(id, "the fixture's first production release of project B failed: %v", err)
		return
	}
	if ok, served := s.edgeServes(key, "production", stable.ID, 20*time.Second); !ok {
		s.gap(id, "the edge never served the fixture's first release (it served %s)", served)
		return
	}
	s.edgeRows = append(s.edgeRows, edgeRow{When: "before approvals are required", Served: "v" + fmt.Sprint(stable.Version), OK: true})

	approvalsOn := s.step(id, "`production` requires two approvals, distinct from the requester", func() error {
		var env map[string]any
		h, err := s.owner.try(http.MethodGet, s.projectPathOf(s.projectB, "/environments/production"), nil, http.StatusOK, &env)
		if err != nil {
			return err
		}
		body := map[string]any{"policy": env["policy"], "approval": map[string]any{
			"n": 2, "from": map[string]any{"role": "reviewer"}, "distinct_from_requester": true,
		}}
		var saved map[string]any
		if _, err := s.owner.try(http.MethodPatch, s.projectPathOf(s.projectB, "/environments/production"), body, http.StatusOK, &saved,
			"If-Match", h.Get("ETag")); err != nil {
			return fmt.Errorf("setting the environment's `approval` was refused: %w", err)
		}
		if _, ok := saved["approval"]; !ok {
			return fmt.Errorf("the environment was saved without its `approval`: the field does not exist, so nothing will wait")
		}
		return nil
	})

	s.touch(s.projectB, s.assigned[1], "Neue Fassung für die Freigabe")
	var request string
	held := s.step(id, "a publish creates a release request and the edge still serves the previous release", func() error {
		var out struct {
			ID        string `json:"id"`
			RequestID string `json:"release_request_id"`
		}
		_, err := s.owner.try(http.MethodPost, s.projectPathOf(s.projectB, "/releases"),
			map[string]any{"environment": "production", "note": "wants approval"}, http.StatusAccepted, &out)
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusCreated {
			// A release was published, not requested. Watch the edge: if
			// the pointer moved, that is the failure §12.3 names.
			_ = json.Unmarshal([]byte(ae.body), &out)
			if moved, _ := s.edgeServes(key, "production", out.ID, 15*time.Second); moved {
				s.edgeRows = append(s.edgeRows, edgeRow{When: "right after the publish", Served: "the new release " + short(out.ID)})
				return fmt.Errorf("the publish answered 201 and moved the pointer at once: the edge serves the new release %s, "+
					"not the previous %s — no request was made and no approval waited", short(out.ID), short(stable.ID))
			}
		}
		// Whatever the API said, the edge decides: the pointer must not
		// have moved.
		if err := staysFor(3*time.Second, func() (string, error) {
			m, ferr := s.fetchManifest(key, "production")
			if ferr != nil {
				return "", ferr
			}
			if m.Release.ID != stable.ID {
				s.edgeRows = append(s.edgeRows, edgeRow{When: "right after the publish", Served: "release " + short(m.Release.ID)})
				return fmt.Sprintf("the pointer moved at once: the edge serves release %s, not the previous %s",
					short(m.Release.ID), short(stable.ID)), nil
			}
			return "", nil
		}); err != nil {
			return err
		}
		s.edgeRows = append(s.edgeRows, edgeRow{When: "right after the publish", Served: "previous release", OK: true})
		if err != nil {
			return missing("a publish into an environment with approvals (202 and a release request)", err)
		}
		request = out.RequestID
		if request == "" {
			request = out.ID
		}
		return nil
	})
	if !approvalsOn || !held {
		s.unreached(id,
			"the requester's own approval is refused",
			"after the first approval the edge still serves the previous release",
			"after the second approval the edge serves the new release",
			"a forced publish over an unmet gate still waits for approvals, and the approvers see its reason")
	} else {
		s.approvalSequence(key, stable.ID, request)
	}

	// Rollback never needs approval and is never delayed (§5.1, §74.2).
	// It runs whatever happened above.
	s.step(id, "a rollback takes effect at the edge at once, with no approval", func() error {
		before, err := s.fetchManifest(key, "production")
		if err != nil {
			return err
		}
		if before.Release.ID == stable.ID {
			s.touch(s.projectB, s.assigned[2], "Noch eine Fassung")
			if _, err := s.publish(s.owner, s.projectB, "production", map[string]any{"note": "to roll back from"}, http.StatusCreated); err != nil {
				if _, err2 := s.publish(s.owner, s.projectB, "production", map[string]any{"note": "to roll back from"}, http.StatusAccepted); err2 != nil {
					return fmt.Errorf("there is no later release to roll back from: %v", err)
				}
				return fmt.Errorf("a release to roll back from could not be put in place without approvals")
			}
		}
		start := time.Now()
		if _, err := s.owner.try(http.MethodPost, s.projectPathOf(s.projectB, "/environments/production/rollbacks"),
			map[string]any{"release_id": stable.ID}, http.StatusOK, nil); err != nil {
			return fmt.Errorf("the rollback was refused: %w", err)
		}
		ok, served := s.edgeServes(key, "production", stable.ID, 10*time.Second)
		if !ok {
			return fmt.Errorf("after the rollback the edge still serves %s", served)
		}
		s.edgeRows = append(s.edgeRows, edgeRow{When: "after a rollback, no approval", OK: true,
			Served: fmt.Sprintf("the earlier release, within %.1fs", time.Since(start).Seconds())})
		return nil
	})
}

// approvalSequence is the part of §12.3 that needs release requests.
func (s *scenario) approvalSequence(key, previous, request string) {
	const id = "12.3"
	decide := func(as *client) error {
		_, err := as.try(http.MethodPost, s.releaseRequestApprovals(s.projectB, request),
			map[string]any{"decision": "granted"}, http.StatusCreated, nil)
		return err
	}
	if !s.step(id, "the requester's own approval is refused", func() error {
		if err := decide(s.owner); err == nil {
			return fmt.Errorf("the requester approved their own release")
		}
		return nil
	}) {
		return
	}
	if !s.step(id, "after the first approval the edge still serves the previous release", func() error {
		if err := decide(s.reviewer1); err != nil {
			return fmt.Errorf("the first approval: %w", err)
		}
		if err := staysFor(3*time.Second, func() (string, error) {
			m, err := s.fetchManifest(key, "production")
			if err != nil {
				return "", err
			}
			if m.Release.ID != previous {
				return "one approval moved the pointer: the edge serves " + short(m.Release.ID), nil
			}
			return "", nil
		}); err != nil {
			return err
		}
		s.edgeRows = append(s.edgeRows, edgeRow{When: "after one approval", Served: "previous release", OK: true})
		return nil
	}) {
		return
	}
	var newID string
	s.step(id, "after the second approval the edge serves the new release", func() error {
		if err := decide(s.reviewer2); err != nil {
			return fmt.Errorf("the second approval: %w", err)
		}
		ok, served := softly(15*time.Second, func() (bool, string) {
			m, err := s.fetchManifest(key, "production")
			if err != nil {
				return false, err.Error()
			}
			newID = m.Release.ID
			return m.Release.ID != previous, "release " + short(m.Release.ID)
		})
		if !ok {
			return fmt.Errorf("two approvals later the edge still serves %s", served)
		}
		s.edgeRows = append(s.edgeRows, edgeRow{When: "after the second approval", Served: "release " + short(newID), OK: true})
		return nil
	})
	s.step(id, "a forced publish over an unmet gate still waits for approvals, and the approvers see its reason", func() error {
		const reason = "The legal copy must ship with the campaign; the reviewers have the PDF."
		// An unmet gate: a German unit back in review.
		if err := s.owner.putTranslation(s.projectPathOf(s.projectB, "/messages/"+s.assigned[3]+"/translations/de"),
			map[string]any{"text": "Noch nicht geprüft", "syntax": "mf2", "state": "needs_review", "origin": "human"}); err != nil {
			return err
		}
		before, err := s.fetchManifest(key, "production")
		if err != nil {
			return fmt.Errorf("the edge's manifest before the forced publish: %w", err)
		}
		var out struct {
			RequestID string `json:"release_request_id"`
		}
		if _, err := s.owner.try(http.MethodPost, s.projectPathOf(s.projectB, "/releases"), map[string]any{
			"environment": "production", "force": true, "force_reason": reason,
		}, http.StatusAccepted, &out); err != nil {
			return fmt.Errorf("the forced publish did not become a request: %w", err)
		}
		if err := staysFor(3*time.Second, func() (string, error) {
			after, err := s.fetchManifest(key, "production")
			if err != nil {
				return "", err
			}
			if after.Release.ID != before.Release.ID {
				return "the forced publish moved the pointer without approvals", nil
			}
			return "", nil
		}); err != nil {
			return err
		}
		var req map[string]any
		if _, err := s.reviewer1.try(http.MethodGet, s.releaseRequestsPath(s.projectB)+"/"+out.RequestID, nil, http.StatusOK, &req); err != nil {
			return fmt.Errorf("an approver could not read the request: %w", err)
		}
		if req["force_reason"] != reason {
			return fmt.Errorf("the approver does not see the force reason (got %v)", req["force_reason"])
		}
		return nil
	})
}
