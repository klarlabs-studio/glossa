package glossa

import (
	"context"
	"net/http"
	"slices"

	"golang.org/x/text/language"
)

// Locale resolution for servers (intent §13, runtimes/SPEC.md §4.1): an
// ordered, pluggable resolver chain whose output is canonicalized and
// matched against the release's locales with RFC 4647 Lookup.

const (
	maxAcceptLanguageBytes = 4096
	maxAcceptLanguageTags  = 16
)

var wildcardTag = language.MustParse("mul")

// Which resolver decided a Negotiation.
const (
	ByExplicit       = "explicit"
	ByUser           = "user"
	ByOrg            = "org"
	ByMetadata       = "metadata"
	ByAcceptLanguage = "accept-language"
	BySource         = "source"
)

// Resolver proposes locales for a request, most preferred first. It
// returns nil when it has no opinion.
type Resolver func(r *http.Request) []string

// ResolverChain resolves a request's locale in a fixed, deterministic
// order: explicit → user → organization → request metadata →
// Accept-Language → the release's source locale. Every resolver is
// optional.
type ResolverChain struct {
	// Explicit is a locale the request names itself, e.g. QueryParam("lang")
	// or PathValue("lang").
	Explicit Resolver
	// User is the signed-in user's preference.
	User Resolver
	// Org is the user's organization preference.
	Org Resolver
	// Metadata is any other request signal, e.g. Cookie("locale") or
	// Header("X-Locale").
	Metadata Resolver
	// IgnoreAcceptLanguage skips the Accept-Language header.
	IgnoreAcceptLanguage bool
}

// Negotiation is the outcome of resolving a request's locale.
type Negotiation struct {
	// Requested are all proposals, canonicalized, in chain order. They go
	// on the request context, so T there picks Locale.
	Requested []string `json:"requested"`
	// Locale is the negotiated locale. With no release loaded it is the
	// first proposal, or "".
	Locale string `json:"locale"`
	// By names the resolver that decided Locale (ByExplicit … BySource).
	By string `json:"by"`
}

type namedResolver struct {
	name    string
	resolve Resolver
}

func (ch ResolverChain) resolvers() []namedResolver {
	out := []namedResolver{{ByExplicit, ch.Explicit}, {ByUser, ch.User}, {ByOrg, ch.Org}, {ByMetadata, ch.Metadata}}
	if !ch.IgnoreAcceptLanguage {
		out = append(out, namedResolver{ByAcceptLanguage, AcceptLanguage})
	}
	return out
}

// Negotiate runs the chain for r against the available locales, falling
// back to source.
func (ch ResolverChain) Negotiate(r *http.Request, available []string, source string) Negotiation {
	n := Negotiation{Requested: []string{}}
	firstBy := ""
	for _, res := range ch.resolvers() {
		if res.resolve == nil {
			continue
		}
		proposals := canonicalizeAll(res.resolve(r))
		if len(proposals) > 0 && firstBy == "" {
			firstBy = res.name
		}
		if match, ok := lookup(proposals, available); ok && n.Locale == "" {
			n.Locale, n.By = match, res.name
		}
		n.Requested = appendNew(n.Requested, proposals)
	}
	switch {
	case n.Locale != "":
	case source != "" || len(n.Requested) == 0:
		n.Locale, n.By = source, BySource
	default:
		n.Locale, n.By = n.Requested[0], firstBy
	}
	return n
}

func appendNew(list, items []string) []string {
	for _, it := range items {
		if !slices.Contains(list, it) {
			list = append(list, it)
		}
	}
	return list
}

// Negotiate resolves r's locale against the active release.
func (c *Client) Negotiate(r *http.Request, chain ResolverChain) Negotiation {
	source := ""
	if rel := c.state.Load().rel; rel != nil {
		source = rel.manifest.SourceLocale
	}
	return chain.Negotiate(r, c.Locales(), source)
}

type negotiationKey struct{}

// Middleware resolves each request's locale with chain and puts it on the
// request context, where T, Localizer and the template functions find it.
// It adds `Vary: Accept-Language` unless the chain ignores that header.
func (c *Client) Middleware(chain ResolverChain) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := c.Negotiate(r, chain)
			if !chain.IgnoreAcceptLanguage {
				w.Header().Add("Vary", "Accept-Language")
			}
			ctx := context.WithValue(r.Context(), negotiationKey{}, n)
			ctx = context.WithValue(ctx, localesKey{}, n.Requested)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// NegotiationFrom returns the negotiation Middleware stored on ctx.
func NegotiationFrom(ctx context.Context) (Negotiation, bool) {
	n, ok := ctx.Value(negotiationKey{}).(Negotiation)
	return n, ok
}

// QueryParam proposes the value of a URL query parameter.
func QueryParam(name string) Resolver {
	return func(r *http.Request) []string { return nonEmpty(r.URL.Query().Get(name)) }
}

// PathValue proposes a net/http pattern wildcard, e.g. {lang} in
// "GET /{lang}/invoices". Wildcards are set by the ServeMux, so use it in
// middleware that wraps the routed handler.
func PathValue(name string) Resolver {
	return func(r *http.Request) []string { return nonEmpty(r.PathValue(name)) }
}

// Cookie proposes the value of a cookie.
func Cookie(name string) Resolver {
	return func(r *http.Request) []string {
		ck, err := r.Cookie(name)
		if err != nil {
			return nil
		}
		return nonEmpty(ck.Value)
	}
}

// Header proposes the value of a request header.
func Header(name string) Resolver {
	return func(r *http.Request) []string { return nonEmpty(r.Header.Get(name)) }
}

// AcceptLanguage proposes the Accept-Language tags by descending quality,
// without wildcards and refusals (q=0).
func AcceptLanguage(r *http.Request) []string {
	header := r.Header.Get("Accept-Language")
	if len(header) > maxAcceptLanguageBytes {
		header = header[:maxAcceptLanguageBytes]
	}
	tags, q, err := language.ParseAcceptLanguage(header)
	if err != nil {
		return nil
	}
	var out []string
	for i, tag := range tags {
		// x/text parses the "*" wildcard as "mul".
		if q[i] > 0 && tag != wildcardTag && len(out) < maxAcceptLanguageTags {
			out = append(out, tag.String())
		}
	}
	return out
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
