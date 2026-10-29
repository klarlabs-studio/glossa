package checkpolicy

import "slices"

// Local overrides (RFC 0005 §4.2, §14 decision 3).
//
// The server owns the policy. `glossa.yaml`'s `check:` block and the
// command's flags are overrides *for the local run*: a developer can
// tighten or loosen their own loop, and cannot change what CI decides.
// The pull-request check never builds an Overrides and never applies
// one — there is nothing local about a check that runs on the server —
// and a test holds the two readers to it.
//
// The type exists so that "local override" is a thing the code has a
// name for rather than a merge buried in one command: something that
// can be printed ("policy: server v7 + local overrides"), tested, and
// kept out of the reader that must not have it.

// Overrides are what a local run may change about the document it
// fetched. Everything else — the rules, the environments, the version,
// the grace, and whether an untranslated key blocks — stays the
// server's, or a pull request and the terminal would part ways on the
// one question the check exists to answer.
type Overrides struct {
	// RequireComplete replaces the document's when it is set.
	RequireComplete Required
	// FailOn replaces the document's when it is not "".
	FailOn Severity
	// Layers, when not nil, are the only layers the run computes
	// (`--layer`). An empty slice is "no layer", which is a run that
	// reports nothing; nil is "every layer the policy leaves on".
	Layers []string
}

// Empty reports whether the overrides change nothing, which is what a
// run with no flags and no `check:` block has.
func (o Overrides) Empty() bool {
	return !o.RequireComplete.Set && o.FailOn == "" && o.Layers == nil
}

// Override returns p as the local run sees it. The document is not
// changed: the result is a copy, and the version it carries is still
// the server's, because an overridden run grades against a policy the
// server issued and says so.
func (p Policy) Override(o Overrides) Policy {
	if o.RequireComplete.Set {
		switch {
		case o.RequireComplete.All:
			p.RequireComplete = nil
		case len(o.RequireComplete.Locales) == 0:
			p.RequireComplete = []string{}
		default:
			p.RequireComplete = slices.Clone(o.RequireComplete.Locales)
		}
	}
	if o.FailOn != "" {
		p.FailOn = o.FailOn
	}
	return p
}

// Selects reports whether the overrides let the layer run. It is the
// command line's half of the question Computes answers for the policy:
// a layer the policy switched off stays off whatever `--layer` says,
// because `off` means the project does not pay for it.
func (o Overrides) Selects(layer string) bool {
	return o.Layers == nil || slices.Contains(o.Layers, layer)
}
