package glossa

import "context"

type localesKey struct{}

// WithLocales returns a copy of ctx carrying the requested locales in
// priority order, canonicalized. Client.T and Client.Localizer read them;
// Middleware sets them for each request.
func WithLocales(ctx context.Context, locales ...string) context.Context {
	return context.WithValue(ctx, localesKey{}, canonicalizeAll(locales))
}

// LocalesFrom returns the requested locales on ctx, or nil.
func LocalesFrom(ctx context.Context) []string {
	locales, _ := ctx.Value(localesKey{}).([]string)
	return locales
}
