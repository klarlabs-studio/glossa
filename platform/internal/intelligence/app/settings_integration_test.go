//go:build integration

package app_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// singleton is one settings resource that exists with defaults at
// version 0 before anyone saves it.
type singleton struct {
	name string
	put  func(w *wiring, ifMatch *int) (version int, err error)
}

func singletons(p uuid.UUID) []singleton {
	budget := domain.MicroUSD(1_000_000)
	policy := app.DefaultRouting()
	auto := []string{"de"}
	return []singleton{
		{"settings", func(w *wiring, ifMatch *int) (int, error) {
			s, err := w.svc.PutSettings(w.admin(), app.SettingsInput{MonthlyBudget: &budget}, ifMatch)
			return s.Version, err
		}},
		{"tenant routing", func(w *wiring, ifMatch *int) (int, error) {
			v, err := w.svc.PutRoutingPolicy(w.admin(), nil, policy, ifMatch)
			return v.Record.Version, err
		}},
		{"project routing", func(w *wiring, ifMatch *int) (int, error) {
			v, err := w.svc.PutRoutingPolicy(w.admin(), &p, policy, ifMatch)
			return v.Record.Version, err
		}},
		{"project settings", func(w *wiring, ifMatch *int) (int, error) {
			s, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{AutoTranslateLocales: &auto}, ifMatch)
			return s.Version, err
		}},
	}
}

// If-Match version 0 — the ETag of never-saved settings — means "create
// only if still absent": the first write wins, a later one fails the
// precondition, and saved settings take their version as usual.
func TestWiringUnsavedSingletonsTakeIfMatchZero(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 0) // saves settings (and a provider the routing needs)
	p := w.project(t, []string{"de"}, nil)
	zero, one, five := 0, 1, 5
	for _, s := range singletons(p) {
		t.Run(s.name, func(t *testing.T) {
			if s.name == "settings" {
				// configure saved them: version 0 is stale now.
				if _, err := s.put(w, &zero); !errors.Is(err, app.ErrPreconditionFailed) {
					t.Fatalf("If-Match 0 on saved settings: %v", err)
				}
				return
			}
			if _, err := s.put(w, &five); !errors.Is(err, app.ErrPreconditionFailed) {
				t.Errorf("If-Match 5 while absent: %v", err)
			}
			if v, err := s.put(w, &zero); err != nil || v != 1 {
				t.Fatalf("create if absent = %d, %v", v, err)
			}
			if _, err := s.put(w, &zero); !errors.Is(err, app.ErrPreconditionFailed) {
				t.Errorf("If-Match 0 once saved: %v", err)
			}
			if v, err := s.put(w, &one); err != nil || v != 2 {
				t.Errorf("update at 1 = %d, %v", v, err)
			}
			if v, err := s.put(w, nil); err != nil || v != 3 {
				t.Errorf("update without If-Match = %d, %v", v, err)
			}
		})
	}
}

// Two writers creating the same settings at once: one wins, the other
// fails the precondition — never a conflict or a lost write.
func TestWiringConcurrentCreatesOfSingletons(t *testing.T) {
	for i, s := range singletons(uuid.Nil) {
		t.Run(s.name, func(t *testing.T) {
			w := newWiring(t, nil)
			if _, _, err := w.svc.CreateProvider(w.admin(), app.ProviderInput{Name: "anthropic", Kind: "anthropic", APIKey: "k"}, ""); err != nil {
				t.Fatal(err)
			}
			p := w.project(t, []string{"de"}, nil)
			s = singletons(p)[i]
			zero := 0
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for n := range errs {
				wg.Go(func() { _, errs[n] = s.put(w, &zero) })
			}
			wg.Wait()
			won, lost := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					won++
				case errors.Is(err, app.ErrPreconditionFailed):
					lost++
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}
			if won != 1 || lost != 1 {
				t.Errorf("won %d, lost %d: %v", won, lost, errs)
			}
		})
	}
}
