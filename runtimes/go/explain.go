package glossa

// Explanations (runtimes/SPEC.md §6): why a message rendered the way it
// did, without side effects.

// Source is where rendered content came from.
type Source string

// Sources, in the SPEC §3 reliability order.
const (
	// SourceMemory: a later refresh brought nothing new (304 or a
	// failure), so the active release is served from memory.
	SourceMemory Source = "memory"
	// SourcePersisted: the active release was restored from the cache
	// directory at startup. The startup load spans New and the first
	// refresh, so a first refresh that fails keeps this source.
	SourcePersisted Source = "persisted"
	// SourceNetwork: the active release was loaded from the edge.
	SourceNetwork Source = "network"
	// SourceBundled: the active release was restored from Config.Bundled
	// at startup (like SourcePersisted).
	SourceBundled Source = "bundled"
	// SourceInline: no loaded locale had the message; the inline default
	// or the message ID was rendered.
	SourceInline Source = "inline"
)

// Outcome is what one fallback step found.
type Outcome string

// Step outcomes.
const (
	OutcomeFound   Outcome = "found"
	OutcomeMissing Outcome = "missing"
	// OutcomeNotLoaded: the locale is in the chain but its artifacts
	// weren't loaded, because Config.Locales left it out.
	OutcomeNotLoaded Outcome = "not-loaded"
)

// Step is one locale of the fallback chain and what resolution found there.
type Step struct {
	Locale  string  `json:"locale"`
	Outcome Outcome `json:"outcome"`
}

// Explanation describes how a message resolves (SPEC §6).
type Explanation struct {
	ID string `json:"id"`
	// Requested are the canonicalized requested locales, in priority order.
	Requested []string `json:"requested"`
	// Locale is the active locale negotiated from Requested.
	Locale string `json:"locale"`
	// Chain is the active locale's fallback chain.
	Chain []string `json:"chain"`
	// ResolvedFrom is the locale the message was found in, or nil when the
	// inline default (or the message ID) is used.
	ResolvedFrom *string `json:"resolvedFrom"`
	// Release is the active release, or nil when nothing is loaded.
	Release *ReleaseRef `json:"release"`
	Source  Source      `json:"source"`
	Steps   []Step      `json:"steps"`
	// Rollout describes the staged rollout in the active manifest (SPEC
	// §1.4): its id and percent, the cohort and the side rendered from. It
	// is nil without a valid rollout, or with rollout support off.
	Rollout *RolloutInfo `json:"rollout"`
}

func (res resolution) explain(snap *snapshot) Explanation {
	e := Explanation{
		ID:        res.id,
		Requested: nonNil(res.requested),
		Locale:    res.locale,
		Chain:     nonNil(res.chain),
		Source:    SourceInline,
		Steps:     nonNil(res.steps),
		Rollout:   snap.rolloutInfo(),
	}
	if snap.rel != nil {
		ref := snap.rel.manifest.Release
		e.Release = &ReleaseRef{ID: ref.ID, Version: ref.Version}
	}
	if res.resolvedFrom != "" {
		from := res.resolvedFrom
		e.ResolvedFrom, e.Source = &from, snap.source
	}
	return e
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
