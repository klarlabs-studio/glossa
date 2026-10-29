//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// Staged rollouts through Release's HTTP edge (RFC 0006 §5.2, §8):
// start (Idempotency-Key), read, list, advance (If-Match), complete and
// abort, and every refusal the contract documents.
func TestRolloutsOverTheAPI(t *testing.T) {
	h := newHarness(t)
	p, v1, v2 := h.rolloutProject(t)
	api := h.api()
	dev := h.as("developer")
	tenant, project := h.tenant.String(), p.String()

	start := func(ctx context.Context, body apiv1.StartRollout, key string) (apiv1.StartRolloutResponseObject, error) {
		params := apiv1.StartRolloutParams{}
		if key != "" {
			params.IdempotencyKey = &key
		}
		return api.StartRollout(ctx, apiv1.StartRolloutRequestObject{Tenant: tenant, Project: project, Environment: "production",
			Params: params, Body: &body})
	}
	ten := apiv1.StartRollout{ReleaseId: v2.ID.String(), Percent: 10}

	if _, err := start(h.as("reviewer"), ten, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a reviewer starting a rollout: %v", err)
	}
	for _, c := range []struct {
		body apiv1.StartRollout
		code string
	}{
		{apiv1.StartRollout{ReleaseId: v2.ID.String(), Percent: 101}, "invalid_percent"},
		{apiv1.StartRollout{ReleaseId: v2.ID.String(), Percent: 10, MaxDurationSeconds: ptr(60)}, "invalid_max_duration"},
		{apiv1.StartRollout{ReleaseId: v2.ID.String(), Percent: 10, MaxDurationSeconds: ptr(0)}, "invalid_max_duration"},
		{apiv1.StartRollout{ReleaseId: v2.ID.String(), Percent: 10, Force: ptr(true)}, "force_reason_required"},
		{apiv1.StartRollout{ReleaseId: uuid.NewString(), Percent: 10}, "release_not_found"},
		{apiv1.StartRollout{ReleaseId: v1.ID.String(), Percent: 10}, "rollout_candidate_served"},
	} {
		if _, err := start(dev, c.body, ""); string(problemCode(t, err)) != c.code {
			t.Errorf("start %+v: %v, want %s", c.body, err, c.code)
		}
	}
	if _, err := api.StartRollout(dev, apiv1.StartRolloutRequestObject{Tenant: tenant, Project: project, Environment: "development",
		Body: &ten}); problemCode(t, err) != "rollout_no_stable" {
		t.Errorf("a rollout where nothing is served: %v", err)
	}

	resp, err := start(dev, ten, "ro-api-1")
	if err != nil {
		t.Fatal(err)
	}
	created := resp.(apiv1.StartRollout201JSONResponse)
	ro := created.Body
	if ro.Status != "active" || ro.Percent != 10 || ro.ReleaseId != v2.ID.String() || ro.StableReleaseId != v1.ID.String() ||
		ro.MaxDurationSeconds != 14*24*3600 || ro.End != nil || *created.Headers.ETag != `"1"` ||
		*created.Headers.Location != "/v1/tenants/"+tenant+"/projects/"+project+"/environments/production/rollouts/"+ro.Id {
		t.Fatalf("started = %+v (%+v)", ro, created.Headers)
	}
	if m := h.rawManifest(t, p, "production"); m["rollout"] == nil {
		t.Fatal("the edge's manifest carries no rollout")
	}
	if again, err := start(dev, ten, "ro-api-1"); err != nil || again.(apiv1.StartRollout201JSONResponse).Headers.IdempotentReplayed == nil {
		t.Errorf("a replayed start: %v", err)
	}
	if _, err := start(dev, ten, ""); problemCode(t, err) != "rollout_active" {
		t.Errorf("a second rollout: %v", err)
	}
	// While it runs, publish and promote into the environment wait.
	if _, err := h.publish(t, p, apiv1.PublishRelease{Environment: "production"}); problemCode(t, err) != "rollout_active" {
		t.Errorf("a publish during a rollout: %v", err)
	}

	get := func(env, id string) (apiv1.GetRolloutResponseObject, error) {
		return api.GetRollout(h.as("reviewer"), apiv1.GetRolloutRequestObject{Tenant: tenant, Project: project, Environment: env, Rollout: id})
	}
	if r, err := get("production", ro.Id); err != nil || r.(apiv1.GetRollout200JSONResponse).Body.Id != ro.Id {
		t.Fatalf("get: %v", err)
	}
	if _, err := get("staging", ro.Id); problemCode(t, err) != "not_found" {
		t.Errorf("a rollout read under another environment: %v", err)
	}
	if _, err := get("production", uuid.NewString()); problemCode(t, err) != "not_found" {
		t.Errorf("an unknown rollout: %v", err)
	}

	advance := func(percent int, ifMatch string) (apiv1.AdvanceRolloutResponseObject, error) {
		return api.AdvanceRollout(dev, apiv1.AdvanceRolloutRequestObject{Tenant: tenant, Project: project, Environment: "production",
			Rollout: ro.Id, Params: apiv1.AdvanceRolloutParams{IfMatch: ifMatch}, Body: &apiv1.AdvanceRollout{Percent: percent}})
	}
	if _, err := advance(50, `"7"`); problemCode(t, err) != "precondition_failed" {
		t.Errorf("a stale If-Match: %v", err)
	}
	if _, err := advance(-1, `"1"`); problemCode(t, err) != "invalid_percent" {
		t.Errorf("percent -1: %v", err)
	}
	ar, err := advance(50, `"1"`)
	if err != nil {
		t.Fatal(err)
	}
	if adv := ar.(apiv1.AdvanceRollout200JSONResponse); adv.Body.Percent != 50 || *adv.Headers.ETag != `"2"` {
		t.Fatalf("advanced = %+v", adv)
	}

	abort := func(id string, ifMatch *string) (apiv1.AbortRolloutResponseObject, error) {
		return api.AbortRollout(dev, apiv1.AbortRolloutRequestObject{Tenant: tenant, Project: project, Environment: "production",
			Rollout: id, Params: apiv1.AbortRolloutParams{IfMatch: ifMatch}})
	}
	if _, err := abort(ro.Id, ptr(`"1"`)); problemCode(t, err) != "precondition_failed" {
		t.Errorf("abort with a stale If-Match: %v", err)
	}
	br, err := abort(ro.Id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := br.(apiv1.AbortRollout200JSONResponse).Body; b.Status != "aborted" || b.End == nil || *b.End != "aborted" || b.EndedBy == nil {
		t.Fatalf("aborted = %+v", b)
	}
	if m := h.rawManifest(t, p, "production"); m["rollout"] != nil {
		t.Fatal("the aborted rollout is still in the manifest")
	}
	if _, err := abort(ro.Id, nil); problemCode(t, err) != "rollout_ended" {
		t.Errorf("aborting twice: %v", err)
	}

	// Again, and complete: the pointer moves to the candidate.
	second, err := start(dev, ten, "")
	if err != nil {
		t.Fatal(err)
	}
	next := second.(apiv1.StartRollout201JSONResponse).Body
	cr, err := api.CompleteRollout(dev, apiv1.CompleteRolloutRequestObject{Tenant: tenant, Project: project, Environment: "production",
		Rollout: next.Id, Params: apiv1.CompleteRolloutParams{IfMatch: ptr(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if c := cr.(apiv1.CompleteRollout200JSONResponse).Body; c.Status != "completed" || *c.End != "completed" {
		t.Fatalf("completed = %+v", c)
	}
	if h.current(t, p, "production") != v2.ID {
		t.Fatal("completing did not move the pointer to the candidate")
	}
	if _, err := api.CompleteRollout(dev, apiv1.CompleteRolloutRequestObject{Tenant: tenant, Project: project, Environment: "production",
		Rollout: next.Id}); problemCode(t, err) != "rollout_ended" {
		t.Errorf("completing twice: %v", err)
	}

	// The history, newest first, a page at a time.
	list := func(size int, token *string) apiv1.ListRollouts200JSONResponse {
		t.Helper()
		resp, err := api.ListRollouts(h.as("reviewer"), apiv1.ListRolloutsRequestObject{Tenant: tenant, Project: project, Environment: "production",
			Params: apiv1.ListRolloutsParams{PageSize: &size, PageToken: token}})
		if err != nil {
			t.Fatal(err)
		}
		return resp.(apiv1.ListRollouts200JSONResponse)
	}
	first := list(1, nil)
	if len(first.Items) != 1 || first.Items[0].Id != next.Id || first.NextPageToken == nil {
		t.Fatalf("first page = %+v", first)
	}
	if rest := list(1, first.NextPageToken); len(rest.Items) != 1 || rest.Items[0].Id != ro.Id || rest.NextPageToken != nil {
		t.Fatalf("second page = %+v", rest)
	}
	if _, err := api.ListRollouts(dev, apiv1.ListRolloutsRequestObject{Tenant: tenant, Project: project, Environment: "production",
		Params: apiv1.ListRolloutsParams{PageToken: ptr("garbage")}}); err == nil {
		t.Error("a forged page token was accepted")
	}
}
