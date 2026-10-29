package glossa

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/messageformat"
)

// Rendering (runtimes/SPEC.md §4.3, §5): resolve through the fallback
// chain, format with the locale the message was found in, and fall back
// to the inline default or the message ID. Rendering never panics and
// never returns an empty string.

// Args are the arguments of a message, by name.
type Args = map[string]any

// Option adjusts one T or Explain call.
type Option func(*callOptions)

type callOptions struct {
	defaultText string
	bidi        bool
	timeZone    *time.Location
	fnOptions   map[string]string // standalone formatters only
}

// Default sets the inline default: the text rendered when no loaded
// locale has the message (SPEC §3, step 5). Without it, the message ID
// is rendered, so a missing string is visible and never blank.
func Default(text string) Option {
	return func(o *callOptions) { o.defaultText = text }
}

// BidiIsolation turns MF2 bidi isolation of placeholders on or off for
// one call. It is on by default, as the MF2 spec requires; turn it off for
// plain-text output such as CLI lines, email subjects and documents (PDF)
// in left-to-right scripts, where the isolation characters would show up
// or be dropped by the renderer.
func BidiIsolation(on bool) Option {
	return func(o *callOptions) { o.bidi = on }
}

// Localizer renders messages for a fixed list of requested locales, for
// example one recipient of a batch email job. It is cheap to create and
// safe for concurrent use; it always renders from the client's current
// release.
type Localizer struct {
	c         *Client
	requested []string
	timeZone  *time.Location // nil: UTC
	cohortKey string         // "": the installation id
}

// For returns a Localizer for locales in priority order. Tags are
// canonicalized (`en_us` → `en-US`, `iw` → `he`); malformed ones are
// ignored. With no usable tag the release's source locale is used.
func (c *Client) For(locales ...string) *Localizer {
	return &Localizer{c: c, requested: canonicalizeAll(locales)}
}

// Localizer returns a Localizer for the locales and the cohort key on ctx
// (see WithLocales, WithCohortKey and Middleware).
func (c *Client) Localizer(ctx context.Context) *Localizer {
	return &Localizer{c: c, requested: LocalesFrom(ctx), cohortKey: CohortKeyFrom(ctx)}
}

// WithCohortKey returns a copy of l that renders from the side of a staged
// rollout key selects (SPEC §1.4), for example one recipient's user ID in a
// batch job. See the package-level WithCohortKey for request contexts.
func (l *Localizer) WithCohortKey(key string) *Localizer {
	c := *l
	c.cohortKey = key
	return &c
}

// snapshot is the client's active snapshot as l's cohort key sees it:
// under a rollout and with a key, rel is the view the key selects.
func (l *Localizer) snapshot() *snapshot {
	snap := l.c.state.Load()
	if snap.ro == nil || l.cohortKey == "" {
		return snap
	}
	v := *snap
	v.rel, v.cohort = snap.pick(l.cohortKey)
	return &v
}

// T renders message id for the locales on ctx.
func (c *Client) T(ctx context.Context, id string, args Args, opts ...Option) string {
	return c.Localizer(ctx).T(id, args, opts...)
}

// Explain reports how id resolves for the locales on ctx, without side
// effects.
func (c *Client) Explain(ctx context.Context, id string) Explanation {
	return c.Localizer(ctx).Explain(id)
}

// T renders message id with args.
func (l *Localizer) T(id string, args Args, opts ...Option) string {
	o := l.options(opts)
	snap := l.snapshot()
	res := snap.rel.resolve(id, l.requested)
	return renderAs(l.c, snap, res, o,
		func(msg messageformat.Message, locale string) (string, string, error) {
			text, err := messageformat.Format(msg, locale, args, o.formatOptions()...)
			return text, text, err
		},
		func(text string) string { return text })
}

func (l *Localizer) options(opts []Option) callOptions {
	o := callOptions{bidi: !l.c.cfg.DisableBidiIsolation, timeZone: l.timeZone}
	if o.timeZone == nil {
		o.timeZone = time.UTC
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// formatOptions are the kernel options of a message call.
func (o callOptions) formatOptions() []messageformat.FormatOption {
	return []messageformat.FormatOption{messageformat.WithBidiIsolation(o.bidi), messageformat.WithTimeZone(o.timeZone)}
}

// Explain reports how id resolves, without side effects (SPEC §6).
func (l *Localizer) Explain(id string) Explanation {
	snap := l.snapshot()
	return snap.rel.resolve(id, l.requested).explain(snap)
}

// Locale returns the active locale: the requested locale the release
// matched, else its source locale. With nothing loaded it is the first
// requested locale, or "".
func (l *Localizer) Locale() string {
	rel := l.snapshot().rel
	if rel == nil {
		if len(l.requested) == 0 {
			return ""
		}
		return l.requested[0]
	}
	return rel.manifest.negotiate(l.requested)
}

// Direction returns the text direction of the active locale, for the HTML
// dir attribute.
func (l *Localizer) Direction() Direction {
	rel := l.snapshot().rel
	if rel == nil {
		return scriptDirection(l.Locale())
	}
	return rel.manifest.direction(l.Locale())
}

// renderAs renders a resolved message with format, which returns the
// output and its text. A missing message, a panic or an empty text render
// inline(the inline default or the message ID) instead, and errors are
// reported, the same way for strings, parts, HTML and runs.
func renderAs[T any](c *Client, snap *snapshot, res resolution, o callOptions,
	format func(msg messageformat.Message, locale string) (T, string, error),
	inline func(text string) T,
) (out T) {
	fallback := res.id
	if o.defaultText != "" {
		fallback = o.defaultText
	}
	defer func() {
		if r := recover(); r != nil {
			c.reportRender(snap, res, ErrorFormat, fmt.Sprintf("formatting panicked: %v", r))
			out = inline(fallback)
		}
	}()
	if res.resolvedFrom == "" {
		if snap.rel != nil {
			c.reportRender(snap, res, ErrorMissingMessage, fmt.Sprintf("no locale in %v has the message", res.chain))
		}
		return inline(fallback)
	}
	v, text, err := format(res.message, res.resolvedFrom)
	if err != nil {
		c.reportRender(snap, res, ErrorFormat, err.Error())
	}
	if text == "" {
		return inline(fallback)
	}
	return v
}

func (c *Client) reportRender(snap *snapshot, res resolution, typ ErrorType, detail string) {
	e := Error{Type: typ, Detail: detail, MessageID: res.id, Locale: res.resolvedFrom}
	if e.Locale == "" {
		e.Locale = res.locale
	}
	if snap.rel != nil {
		e.ReleaseID = snap.rel.manifest.Release.ID
	}
	c.reporter.report(e)
}
