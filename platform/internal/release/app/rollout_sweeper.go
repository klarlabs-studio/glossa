package app

import (
	"context"
	"errors"
	"log/slog"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// principalRolloutSweeper is the background principal that aborts
// rollouts past their max_duration; its events name it as their actor.
const principalRolloutSweeper = "release.rollout_sweeper"

// rolloutSweepBatch bounds the expired rollouts one query returns.
const rolloutSweepBatch = 100

// RolloutMetrics records rollout state (RFC 0006 §10.1).
type RolloutMetrics interface {
	// ActiveRollouts reports how many rollouts are active across the
	// deployment, as the sweep last counted them.
	ActiveRollouts(n int)
}

// RolloutSweeper aborts rollouts past their max_duration (RFC 0006
// §5.2), so a forgotten 10 % doesn't become a permanent second
// production. It runs on the kernel scheduler's lease, so one replica
// sweeps; each rollout is aborted in its tenant's scope as the
// background principal release.rollout_sweeper, which holds
// releases.read and releases.publish, and catalog.read to see that the
// project still exists, and nothing else.
type RolloutSweeper struct {
	s       *Service
	scanner Scanner
	metrics RolloutMetrics
}

// NewRolloutSweeper returns a sweeper; metrics may be nil.
func NewRolloutSweeper(s *Service, scanner Scanner, metrics RolloutMetrics) *RolloutSweeper {
	return &RolloutSweeper{s: s, scanner: scanner, metrics: metrics}
}

// Sweep aborts every rollout expired on the Service's clock and returns
// how many it aborted. One failing rollout doesn't hold up the others;
// their errors are joined, and the next sweep finds them again.
func (w *RolloutSweeper) Sweep(ctx context.Context) (int, error) {
	n := 0
	var errs []error
	seen := map[RolloutRef]bool{}
	for {
		refs, err := w.scanner.ExpiredRollouts(ctx, w.s.now(), rolloutSweepBatch)
		if err != nil {
			return n, errors.Join(append(errs, err)...)
		}
		fresh := 0
		for _, ref := range refs {
			if seen[ref] { // failed earlier in this sweep; left for the next
				continue
			}
			seen[ref] = true
			fresh++
			expired, err := w.expire(ctx, ref)
			if err != nil {
				w.s.logger.WarnContext(ctx, "release: expired rollout not aborted",
					slog.String("project_id", ref.Project.String()), slog.String("environment", ref.Environment),
					slog.String("rollout_id", ref.Rollout.String()), slog.Any("error", err))
				errs = append(errs, err)
			}
			if expired {
				n++
			}
		}
		if fresh == 0 || len(refs) < rolloutSweepBatch {
			break
		}
	}
	w.count(ctx)
	return n, errors.Join(errs...)
}

func (w *RolloutSweeper) expire(ctx context.Context, ref RolloutRef) (bool, error) {
	ctx = tenancy.ContextWithTenant(context.WithoutCancel(ctx), ref.Tenant)
	bg, err := authz.Background(ctx, principalRolloutSweeper, authz.ReleasesRead, authz.ReleasesPublish, authz.CatalogRead)
	if err != nil {
		return false, err
	}
	return w.s.expireRollout(bg, ref)
}

// count reports the active rollouts to the metrics, if any. A failed
// count leaves the gauge as it was.
func (w *RolloutSweeper) count(ctx context.Context) {
	if w.metrics == nil {
		return
	}
	n, err := w.scanner.ActiveRollouts(ctx)
	if err != nil {
		w.s.logger.WarnContext(ctx, "release: active rollouts not counted", slog.Any("error", err))
		return
	}
	w.metrics.ActiveRollouts(n)
}
