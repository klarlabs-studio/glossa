//go:build integration

package main

import (
	"cmp"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// drainBudget is how long the outbox may take to deliver everything one
// 500-item write left behind, against a local Postgres. Delivered one
// event per transaction per subscriber, a step took 5–19 s to drain
// (#89, ~35 s for the four); batched, 0.7–7.6 s (~12–18 s). The bound
// is generous for slow CI runners; the timings are logged, and a batch
// that silently fell back to single deliveries fails the test on its
// own. GLOSSA_DRAIN_BUDGET overrides it.
func drainBudget(t *testing.T) time.Duration {
	if v := os.Getenv("GLOSSA_DRAIN_BUDGET"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("GLOSSA_DRAIN_BUDGET: %v", err)
		}
		return d
	}
	return 30 * time.Second
}

// TestOutboxDrainThroughput pushes and imports full batches through the
// real server and measures how long the dispatcher takes to deliver
// every event they left, then checks nothing was dead-lettered and the
// audit chain recorded each delivered event once, gap-free and linked,
// and that no handler failed (so no batch fell back to single
// deliveries). It logs each subscriber's handler time.
func TestOutboxDrainThroughput(t *testing.T) {
	s := startServer(t)
	p, secret := perfProject(t, s)
	s.waitDrained(t, time.Minute) // the setup's own events

	const n = 500
	budget := drainBudget(t)
	step := func(name, path string, items []map[string]any) {
		t.Helper()
		s.do(call{method: "POST", path: path, bearer: secret, body: map[string]any{"items": items}}).
			want(t, http.StatusOK, "")
		pending := s.pendingEvents(t)
		took := s.waitDrained(t, budget)
		t.Logf("%s: %d events drained in %v", name, pending, took)
	}
	step("message-upserts create", p+"/message-upserts", perfMessages(n, 1))
	step("translation-imports create", p+"/translation-imports", perfTranslations(n, 1))
	step("message-upserts revise (translations go outdated)", p+"/message-upserts", perfMessages(n, 2))
	step("translation-imports revise", p+"/translation-imports", perfTranslations(n, 2))

	var dead int
	if err := s.db.Super.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE status = 'dead'`).Scan(&dead); err != nil {
		t.Fatal(err)
	}
	if dead != 0 {
		t.Errorf("%d events dead-lettered", dead)
	}
	s.checkAuditChains(t)
	s.logHandlerTime(t)
	var fallbacks []string
	for line := range strings.Lines(s.logs.String()) {
		if strings.Contains(line, "handler failed") {
			fallbacks = append(fallbacks, line)
		}
	}
	if len(fallbacks) > 0 {
		t.Errorf("%d handler failures, first: %s", len(fallbacks), fallbacks[0])
	}
}

// logHandlerTime logs the time each subscriber spent handling events,
// from the dispatcher's metrics, so a slow drain names its cause.
func (s *server) logHandlerTime(t *testing.T) {
	t.Helper()
	resp, err := http.Get(s.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	total := map[string]float64{}
	const metric = "glossa_outbox_handler_duration_seconds_sum{"
	for line := range strings.Lines(string(body)) {
		if !strings.HasPrefix(line, metric) {
			continue
		}
		labels, value, _ := strings.Cut(strings.TrimPrefix(line, metric), "} ")
		_, sub, _ := strings.Cut(labels, `subscriber="`)
		sub, _, _ = strings.Cut(sub, `"`)
		v, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		total[sub] += v
	}
	subs := slices.SortedFunc(maps.Keys(total), func(a, b string) int { return cmp.Compare(total[b], total[a]) })
	for _, sub := range subs {
		t.Logf("handler time %-36s %8.2fs", sub, total[sub])
	}
}

// pendingEvents counts the events not yet delivered.
func (s *server) pendingEvents(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.db.Super.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events WHERE status = 'pending'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// waitDrained waits until no event is pending and returns how long that
// took, failing the test past budget.
func (s *server) waitDrained(t *testing.T, budget time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for s.pendingEvents(t) > 0 {
		if took := time.Since(start); took > budget {
			t.Fatalf("outbox not drained after %v: %d events pending", took, s.pendingEvents(t))
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Since(start)
}

// checkAuditChains checks every tenant's audit chain is gap-free and
// linked, and holds exactly one entry per event delivered to it.
func (s *server) checkAuditChains(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	var broken int
	if err := s.db.Super.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT sequence, prev_hash, hash,
			       lag(hash) OVER w AS prev, lag(sequence) OVER w AS prev_seq
			FROM audit_entries WINDOW w AS (PARTITION BY tenant_id ORDER BY sequence)
		) c
		WHERE (prev_seq IS NULL AND sequence <> 1)
		   OR (prev_seq IS NOT NULL AND (sequence <> prev_seq + 1 OR prev_hash <> prev))`).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if broken != 0 {
		t.Errorf("%d audit entries break their chain", broken)
	}
	var delivered, recorded, missing int
	if err := s.db.Super.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM outbox_events WHERE 'audit.record' = ANY (delivered_to)),
		  (SELECT count(*) FROM audit_entries WHERE source = 'outbox'),
		  (SELECT count(*) FROM outbox_events o WHERE 'audit.record' = ANY (o.delivered_to)
		     AND NOT EXISTS (SELECT 1 FROM audit_entries a WHERE a.tenant_id = o.tenant_id AND a.event_id = o.id))`).
		Scan(&delivered, &recorded, &missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 || recorded != delivered {
		t.Errorf("audit: %d events delivered, %d entries recorded, %d missing", delivered, recorded, missing)
	}
	t.Logf("audit: %d entries, chains intact", recorded)
}
