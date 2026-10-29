package memory

import (
	"context"
	"fmt"
	"sync"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Budget is an in-process per-tenant spending cap. Check refuses a call
// whose upper-bound estimate would take the tenant over its cap; Record
// books the actual cost. The wiring wave replaces it with a persistent,
// per-period budget.
type Budget struct {
	mu    sync.Mutex
	caps  map[string]domain.MicroUSD
	spent map[string]domain.MicroUSD
	log   []domain.Spend
}

var _ domain.BudgetGuard = (*Budget)(nil)

// NewBudget returns a guard with caps by tenant; a tenant without a cap
// is refused.
func NewBudget(caps map[string]domain.MicroUSD) *Budget {
	return &Budget{caps: caps, spent: map[string]domain.MicroUSD{}}
}

// Check implements domain.BudgetGuard.
func (b *Budget) Check(_ context.Context, scope domain.Scope, estimate domain.MicroUSD) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit, ok := b.caps[scope.TenantID]
	if !ok || b.spent[scope.TenantID]+estimate > limit {
		return fmt.Errorf("%w: tenant %s has spent %v of %v; this call may cost up to %v",
			domain.ErrBudgetExceeded, scope.TenantID, b.spent[scope.TenantID], limit, estimate)
	}
	return nil
}

// Record implements domain.BudgetGuard.
func (b *Budget) Record(_ context.Context, scope domain.Scope, s domain.Spend) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent[scope.TenantID] += s.Cost
	b.log = append(b.log, s)
	return nil
}

// Spent returns what tenant has spent.
func (b *Budget) Spent(tenant string) domain.MicroUSD {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent[tenant]
}

// Spends returns the recorded calls.
func (b *Budget) Spends() []domain.Spend {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]domain.Spend(nil), b.log...)
}
