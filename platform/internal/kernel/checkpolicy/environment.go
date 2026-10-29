package checkpolicy

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// The policy document's per-environment block (RFC 0005 §4.1):
// production may require locales a branch does not, and a review state
// a branch does not.

// ReviewApproved is the only review state a policy can ask for today:
// every translation the release publishes has been approved.
const ReviewApproved = "approved"

// Required is a `require_complete` as JSON spells it, where three
// things have to stay apart and two of them look alike:
//
//	absent      inherit the document's (Set is false)
//	null        every locale (All)
//	["de","en"] those locales, and an empty list is "none"
//
// The document's own require_complete is a plain []string, where nil is
// "every locale", because it has nothing to inherit from. An
// environment's has to be able to say "I did not say", so it is this.
type Required struct {
	// Set says the block named a require_complete at all.
	Set bool
	// All is `null`: every locale must be complete.
	All bool
	// Locales are the required ones when All is false.
	Locales []string
}

// AllLocales returns a Required naming every locale.
func AllLocales() Required { return Required{Set: true, All: true} }

// RequiredLocales returns a Required naming exactly these locales. An empty
// list is "no locale has to be complete".
func RequiredLocales(locales ...string) Required {
	return Required{Set: true, Locales: append([]string{}, locales...)}
}

// Equal compares two require_completes.
func (r Required) Equal(o Required) bool {
	return r.Set == o.Set && r.All == o.All && slices.Equal(r.Locales, o.Locales)
}

// MarshalJSON writes null for "every locale" and a list otherwise.
func (r Required) MarshalJSON() ([]byte, error) {
	if r.All {
		return []byte("null"), nil
	}
	if r.Locales == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(r.Locales)
}

// UnmarshalJSON reads null as "every locale" and a list as itself.
// Absence is not seen here at all — it is the zero Required, which is
// what "the block did not say" means.
func (r *Required) UnmarshalJSON(b []byte) error {
	*r = Required{Set: true}
	if string(b) == "null" {
		r.All = true
		return nil
	}
	var locales []string
	if err := json.Unmarshal(b, &locales); err != nil {
		return err
	}
	if locales == nil {
		locales = []string{}
	}
	r.Locales = locales
	return nil
}

// Environment is what one environment asks for beyond the document's
// base. Rules select on the environment too (Selector.Environment);
// this block is for the questions a rule cannot answer, because they
// are about the project rather than about a finding.
type Environment struct {
	// RequireComplete replaces the document's for this environment, or
	// inherits it when the block does not name one.
	RequireComplete Required
	// RequireReview is the review state a release must have reached to
	// publish here: ReviewApproved, or "" for none.
	RequireReview string
}

// Equal compares two environment blocks.
func (e Environment) Equal(o Environment) bool {
	return e.RequireComplete.Equal(o.RequireComplete) && e.RequireReview == o.RequireReview
}

// MarshalJSON writes only what the block says, so a round trip cannot
// turn "I did not say" into "every locale".
func (e Environment) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if e.RequireComplete.Set {
		out["require_complete"] = e.RequireComplete
	}
	if e.RequireReview != "" {
		out["require_review"] = e.RequireReview
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads a block, leaving what it does not say unset.
func (e *Environment) UnmarshalJSON(b []byte) error {
	// Required is an Unmarshaler, so a literal null reaches it and is
	// read as "every locale"; a member that is not there at all is never
	// visited and stays unset, which is "inherit".
	var raw struct {
		RequireComplete Required `json:"require_complete"`
		RequireReview   string   `json:"require_review"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*e = Environment{RequireComplete: raw.RequireComplete, RequireReview: raw.RequireReview}
	return nil
}

// validateEnvironments checks every block and returns them normalized.
func validateEnvironments(envs map[string]Environment, known []string) (map[string]Environment, error) {
	if envs == nil {
		return nil, nil
	}
	out := make(map[string]Environment, len(envs))
	names := make([]string, 0, len(envs))
	for name := range envs {
		names = append(names, name)
	}
	// Sorted, so a policy with two bad environments reports the same one
	// every time.
	sort.Strings(names)
	for _, name := range names {
		e := envs[name]
		if name == "" {
			return nil, fmt.Errorf("%w: an environment needs a name", ErrInvalidEnvironment)
		}
		if e.RequireReview != "" && e.RequireReview != ReviewApproved {
			return nil, fmt.Errorf("%w: %s: require_review is %q, not %q",
				ErrInvalidEnvironment, name, e.RequireReview, ReviewApproved)
		}
		if e.RequireComplete.Set && !e.RequireComplete.All {
			if err := requireKnown(e.RequireComplete.Locales, known); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if e.RequireComplete.Locales == nil {
				e.RequireComplete.Locales = []string{}
			}
		}
		out[name] = e
	}
	return out, nil
}
