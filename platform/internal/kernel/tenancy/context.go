package tenancy

import "context"

type ctxKey struct{}

// ContextWithTenant returns a context scoped to tenant id.
//
// The tenant of a request is derived server-side only, from the
// authenticated session or token (product standard §2). Call this from
// [Middleware] (via a [Resolver]), from the outbox dispatcher when it
// replays an event, and from tests — never with a value taken from a
// request header, path or body.
func ContextWithTenant(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the tenant the context is scoped to. ok is false
// when there is none (or it is the zero ID).
func FromContext(ctx context.Context) (id ID, ok bool) {
	id, _ = ctx.Value(ctxKey{}).(ID)
	return id, !id.IsZero()
}
