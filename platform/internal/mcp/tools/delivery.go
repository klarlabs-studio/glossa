package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	glossa "go.klarlabs.de/glossa/runtimes/go"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// ExplainDeliveryName is the delivery explanation tool's wire name.
const ExplainDeliveryName = "explain_delivery"

type explainDeliveryArgs struct {
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	Release     string   `json:"release"`
	Locale      string   `json:"locale"`
	Locales     []string `json:"locales"`
	Key         string   `json:"key"`
}

// Step is one locale of the fallback chain and what was found there.
// The outcomes are the runtime's own (runtimes/SPEC.md §6), so a server
// explanation and a runtime explain() read the same.
type Step struct {
	Locale string `json:"locale"`
	// Outcome is "found" when the release's artifact for that locale
	// carries the key, "missing" when it does not, and "not-checked"
	// when no key was asked about.
	Outcome string `json:"outcome"`
}

// DeliveryExplanation answers "why does this locale resolve this way
// for this release" (RFC 0005 §7.3).
//
// Omitted: the manifest's signatures and artifact digests, the
// release's stats, and the message's text — this tool explains
// resolution, and message_get reads text.
type DeliveryExplanation struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Release     string `json:"release"`
	Version     int    `json:"version"`
	// Digest is the manifest digest: two releases with equal digests
	// serve exactly the same text.
	Digest    string `json:"digest"`
	CreatedAt string `json:"created_at,omitempty"`
	// Requested are the locales asked for, canonicalized and deduplicated.
	Requested []string `json:"requested"`
	// Locale is the active locale RFC 4647 Lookup picked over the
	// release's locales, and Chain the order a message is looked for in.
	Locale string   `json:"locale"`
	Chain  []string `json:"chain"`
	// SourceLocale is where the chain always ends, and Locales what the
	// release ships.
	SourceLocale string   `json:"source_locale"`
	Locales      []string `json:"locales"`
	// Key and Namespace are the message asked about, when one was.
	Key       string `json:"key,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// ResolvedFrom is the locale the message is served from, empty when
	// no locale in the chain has it.
	ResolvedFrom string `json:"resolved_from,omitempty"`
	Steps        []Step `json:"steps"`
}

// outcomes of a step.
const (
	outcomeFound      = "found"
	outcomeMissing    = "missing"
	outcomeNotChecked = "not-checked"
)

// explainDelivery explains what an environment serves and how a locale
// resolves in it. Negotiation is the Go runtime's own (glossa.ResolveLocale,
// runtimes/SPEC.md §4.1–§4.2): the control plane must not answer this
// question differently from the runtime that actually serves it.
func explainDelivery(d Delivery, c Catalog) app.Tool {
	return app.Tool{
		Name:  ExplainDeliveryName,
		Title: "Explain delivery",
		Description: "Explain what a release serves and why a locale resolves the way it does: the " +
			"release an environment currently serves, the active locale negotiated from the ones " +
			"asked for, its fallback chain, and — with a key — which locale of that chain actually " +
			"carries the message. Uses the runtime's own negotiation, so it answers exactly what a " +
			"client would see.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermReleasesRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(explainDeliverySchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a explainDeliveryArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			release, err := optionalID("release", a.Release)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("environment", a.Environment); err != nil {
				return app.Result{}, err
			}
			requested := a.Locales
			if a.Locale != "" {
				requested = append([]string{a.Locale}, requested...)
			}
			if len(requested) == 0 {
				return app.Result{}, invalid("locale", "is required (or locales)")
			}
			if len(requested) > MaxLimit {
				return app.Result{}, invalid("locales", fmt.Sprintf("at most %d locales", MaxLimit))
			}
			served, err := d.Served(ctx, project, a.Environment, release)
			if err != nil {
				return app.Result{}, err
			}
			out := explain(served, a, requested)
			if err := resolveKey(ctx, d, c, project, a.Key, served, &out); err != nil {
				return app.Result{}, err
			}
			return app.Result{Explanation: sentence(out), Data: out}, nil
		},
	}
}

// explain negotiates the locale and builds the answer's frame.
func explain(served Served, a explainDeliveryArgs, requested []string) DeliveryExplanation {
	canonical, active, chain := glossa.ResolveLocale(glossa.LocaleSet{
		SourceLocale: served.SourceLocale, Locales: served.Locales, Fallback: served.Fallback,
	}, requested)
	out := DeliveryExplanation{
		Project: a.Project, Environment: served.Environment, Release: served.ReleaseID, Version: served.Version,
		Digest: served.Digest, CreatedAt: served.CreatedAt, Requested: canonical, Locale: active, Chain: chain,
		SourceLocale: served.SourceLocale, Locales: served.Locales, Steps: []Step{},
	}
	if out.Requested == nil {
		out.Requested = []string{}
	}
	return out
}

// resolveKey walks the chain for a key, if one was asked about. A
// message is in a locale's artifact only when that locale has a usable
// translation of it, so this is where "why is this English" is
// answered.
func resolveKey(
	ctx context.Context, d Delivery, c Catalog, project uuid.UUID, key string, served Served, out *DeliveryExplanation,
) error {
	if key == "" {
		for _, l := range out.Chain {
			out.Steps = append(out.Steps, Step{Locale: l, Outcome: outcomeNotChecked})
		}
		return nil
	}
	m, err := c.Message(ctx, project, key)
	if err != nil {
		return err
	}
	out.Key, out.Namespace = m.Key, namespaceOrDefault(m.Namespace)
	id, err := uuid.Parse(served.ReleaseID)
	if err != nil {
		return fmt.Errorf("%w: the release has no usable id", domain.ErrNotFound)
	}
	for _, locale := range out.Chain {
		outcome := outcomeMissing
		if digest := served.Artifacts[locale][out.Namespace]; digest != "" && out.ResolvedFrom == "" {
			has, err := d.ArtifactHasMessage(ctx, project, id, digest, key)
			if err != nil {
				return err
			}
			if has {
				outcome, out.ResolvedFrom = outcomeFound, locale
			}
		}
		out.Steps = append(out.Steps, Step{Locale: locale, Outcome: outcome})
	}
	return nil
}

// namespaceOrDefault is the artifact namespace of a message: a message
// without one ships in "default" (runtimes/SPEC.md §1.2).
func namespaceOrDefault(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}

// sentence is the one-line explanation beside the payload.
func sentence(e DeliveryExplanation) string {
	head := fmt.Sprintf("%s serves release %d; %v resolves to %s (chain %v)",
		e.Environment, e.Version, e.Requested, e.Locale, e.Chain)
	switch {
	case e.Key == "":
		return head + "."
	case e.ResolvedFrom == "":
		return fmt.Sprintf("%s, and no locale in the chain carries %s.", head, e.Key)
	default:
		return fmt.Sprintf("%s, and %s is served from %s.", head, e.Key, e.ResolvedFrom)
	}
}

const explainDeliverySchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 63, "description": "The environment, e.g. \"production\"."},
    "release": {"type": "string", "format": "uuid", "description": "Explain this release instead of the one the environment serves now."},
    "locale": {"type": "string", "maxLength": 35, "description": "The locale asked for, as a client would send it."},
    "locales": {"type": "array", "maxItems": 100, "items": {"type": "string", "maxLength": 35}, "description": "Further locales in priority order, as an Accept-Language list."},
    "key": {"type": "string", "maxLength": 200, "description": "Also say which locale of the chain actually carries this message."}
  },
  "required": ["project", "environment"],
  "additionalProperties": false
}`
