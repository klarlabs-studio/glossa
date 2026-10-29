package tools

import (
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
)

// Read returns the read tools of RFC 0005 §7.3, in the order a client
// sees them. Every one of them is in the read toolset, so a write
// session gets them too and a read session gets nothing else: the
// toolset gate in app.Service is what enforces that, and these tools
// declare only where they belong.
//
// A tool whose port is nil is left out. A deployment that runs without
// a context should not advertise a tool that cannot answer, and an
// agent that never sees a tool wastes no call discovering it is broken.
func Read(s Sources) []app.Tool {
	var out []app.Tool
	if s.Catalog != nil {
		out = append(out, catalogSearch(s.Catalog), messageGet(s.Catalog, s.Usages))
	}
	if s.Translations != nil {
		out = append(out, translationGet(s.Translations))
	}
	if s.Usages != nil {
		out = append(out, usagesGet(s.Usages))
	}
	if s.Knowledge != nil {
		out = append(out, tmSearch(s.Knowledge), termLookup(s.Knowledge), styleRules(s.Knowledge))
	}
	if s.Quality != nil {
		out = append(out, findingsList(s.Quality))
	}
	if s.Checks != nil {
		out = append(out, checkRun(s.Checks))
	}
	if s.Delivery != nil && s.Catalog != nil {
		out = append(out, explainDelivery(s.Delivery, s.Catalog))
	}
	// RFC 0006 §8's: assignments, workflow state, release requests,
	// and the quality numbers and rollouts beside them.
	return append(out, Operations(s)...)
}

// Write returns the write tools of RFC 0005 §7.3, in the order a client
// sees them, and it is the *first* of the two locks on a write only in
// the sense that it decides what exists at all. The locks themselves
// are elsewhere and are both app.Service's: a write session needs the
// token's `write` scope (Service.AllowToolset, checked when the session
// opens), and a write tool needs a session that opened the write
// toolset (Service.run, checked on every call). These tools declare
// domain.ToolsetWrite and the permission their context checks anyway,
// and enforce nothing themselves — a tool that policed its own access
// would be a second implementation of the rule.
//
// `check_run` is not here. It changes nothing and stores nothing, so it
// is a read tool (RFC 0005 §7.3 gives it the `read` scope) and a
// read-only session runs it.
//
// The release tools are not here either. `publish` is its own scope,
// and so its own toolset: see Publish.
func Write(s Sources) []app.Tool {
	var out []app.Tool
	if s.Messages != nil {
		out = append(out, messageUpsert(s.Messages))
	}
	if s.Proposals != nil {
		out = append(out, translationPropose(s.Proposals))
	}
	if s.Locales != nil {
		out = append(out, localeAdd(s.Locales))
	}
	if s.Translator != nil {
		out = append(out, translate(s.Translator))
	}
	return out
}

// All returns every tool a deployment serves, one toolset at a time:
// read, then write, then publish. It is what a server registers — the
// toolset gate, not the registry, decides which of them a session may
// call.
func All(s Sources) []app.Tool { return append(append(Read(s), Write(s)...), Publish(s)...) }
