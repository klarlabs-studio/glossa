// Package style adapts Knowledge's effective style guide to the shape
// the style layer grades: its mechanical fields, and only those.
//
// RFC 0005 §3.2 draws the line and this package is where it is drawn. A
// guide has mechanical fields — a formality, a pair of quotation marks,
// a dash, an ellipsis, whether a unit takes a space, number and date
// conventions beyond CLDR — and it has prose rules, each a rule with a
// rationale and some good and bad examples. The mechanical fields cross
// this boundary. The prose rules do not, and they cannot: layers.
// StyleGuide has nowhere to put them, because "a regex over a rationale
// would be a lie about what the system knows" and a type that cannot
// carry the rationale is a type nobody can be tempted to grade it from.
//
// The prose rules are not lost. They stay where they already are: in
// the translation agent's prompt (RFC 0003 §3.2) and in the linguistic
// layer's evidence, which is the layer that can actually read them.
package style

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// Knowledge is the port this package adapts: Intelligence's own adapter
// onto the Knowledge context already has it, which is why the style
// layer's guide arrives in Intelligence's shape and is converted here
// rather than being resolved a second way.
type Knowledge interface {
	EffectiveStyle(
		ctx context.Context, scope inteldomain.Scope, locale, namespace string,
	) (inteldomain.StyleGuide, error)
}

// Port resolves the effective style guide for a project's locales.
type Port struct{ knowledge Knowledge }

// New returns the port.
func New(k Knowledge) *Port { return &Port{knowledge: k} }

// EffectiveStyle is locale's merged guide, reduced to what the style
// layer grades. It answers ok=false where the project has no guide for
// the locale, which the layer reads as "nothing to check against" — not
// as a clean bill.
func (p *Port) EffectiveStyle(ctx context.Context, project uuid.UUID, locale string) (layers.StyleGuide, bool) {
	if p == nil || p.knowledge == nil {
		return layers.StyleGuide{}, false
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return layers.StyleGuide{}, false
	}
	// The namespace is empty: the layer grades a project's locales, and
	// a per-namespace guide is resolved when a namespace is being
	// translated. Narrowing here would give one namespace's guide to
	// every message in the project, which is worse than the project's.
	g, err := p.knowledge.EffectiveStyle(
		ctx, inteldomain.Scope{TenantID: tenant.String(), ProjectID: project.String()}, locale, "")
	if err != nil {
		return layers.StyleGuide{}, false
	}
	out := Mechanical(g)
	return out, out.Stated()
}

// Mechanical is the guide's mechanical half, and nothing else.
//
// Knowledge stores the punctuation and number fields as a string map,
// because a style guide is edited as a document. Reading it back into
// typed fields here is what lets the layer check them without parsing
// strings at every rule.
func Mechanical(g inteldomain.StyleGuide) layers.StyleGuide {
	out := layers.StyleGuide{Version: g.Version, Formality: g.Formality}
	if g.Pronoun != "" {
		out.Pronouns = []string{g.Pronoun}
	}
	get := func(k string) (string, bool) {
		v, ok := g.Punctuation[k]
		return v, ok && v != ""
	}
	if v, ok := get("quotes"); ok {
		out.QuoteOpen, out.QuoteClose = quotePair(v)
	}
	if v, ok := get("dash"); ok {
		out.Dash = v
	}
	if v, ok := get("ellipsis"); ok {
		out.Ellipsis = v
	}
	if v, ok := get("space_before_unit"); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			out.SpaceBeforeUnit = &b
		}
	}
	if v, ok := get("decimal_separator"); ok {
		out.Decimal = v
	}
	if v, ok := get("grouping_separator"); ok {
		out.Group = v
	}
	if v, ok := get("date_format"); ok {
		out.DateOrder = dateOrder(v)
	}
	return out
}

// quotePair splits a stored pair of quotation marks. A guide writes
// them as one field — `«…»`, `""`, `„“` — and the layer needs the two
// halves apart.
func quotePair(v string) (open, close string) {
	if i := strings.Index(v, "…"); i > 0 {
		return v[:i], v[i+len("…"):]
	}
	r := []rune(v)
	switch len(r) {
	case 0:
		return "", ""
	case 1:
		return string(r[0]), string(r[0])
	}
	return string(r[0]), string(r[len(r)-1])
}

// dateOrder reads a stored date format as a field order. A guide that
// writes something this cannot read states no order, and the layer
// grades nothing rather than guessing at one.
func dateOrder(format string) string {
	f := strings.ToLower(format)
	var order []rune
	for _, r := range f {
		switch r {
		case 'y', 'm', 'd':
			if len(order) == 0 || order[len(order)-1] != r {
				order = append(order, r)
			}
		}
	}
	switch string(order) {
	case "ymd", "dmy", "mdy":
		return string(order)
	}
	return ""
}
