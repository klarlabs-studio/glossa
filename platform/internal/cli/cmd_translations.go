package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// glossa translations review (issue #74): a person's or an agent's
// review decision on translation units, through the API's
// reviewTranslation. Approving and rejecting need translations.review
// for the locale, the other states translations.write. Whether the
// reviewer may review their own text is the server's decision (issue
// #73): the CLI never second-guesses it.

const translationsReviewSchema = "glossa.cli.translations.review/v1"

// reviewTargetStates are the review states --state takes.
var reviewTargetStates = []string{"approved", "rejected", "draft", "needs_review"}

// ── output shapes ───────────────────────────────────────────────────

type reviewErrorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Why     string `json:"why,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// translationReviewJSON is the outcome for one unit. Error is set when
// the unit was not reviewed.
type translationReviewJSON struct {
	Key    string `json:"key"`
	Locale string `json:"locale"`
	// From is the state the unit was in when it was read last.
	From     string           `json:"from,omitempty"`
	State    string           `json:"state,omitempty"`
	Revision int              `json:"revision,omitempty"`
	Outdated bool             `json:"outdated,omitempty"`
	Retried  bool             `json:"retried,omitempty"`
	Error    *reviewErrorJSON `json:"error,omitempty"`
}

type translationsReviewDoc struct {
	Schema string `json:"schema"`
	// State is the state asked for.
	State    string                  `json:"state"`
	Reviewed int                     `json:"reviewed"`
	Failed   int                     `json:"failed"`
	Units    []translationReviewJSON `json:"units"`
}

// ── arguments ───────────────────────────────────────────────────────

const translationsUsage = `translations review <key>@<locale>... --state approved|rejected|draft|needs_review [flags]

  glossa translations review checkout.pay@de --state approved
  glossa translations review checkout.pay@de cart.items@de --state rejected

Moves each translation unit to --state and records you as the reviewer; the text and its
provenance stay. Approving and rejecting need translations.review for the locale, the other
states translations.write. The unit is read first and the review is made against what was
read (If-Match); when it changed in between, it is read again and tried once more.
Each unit is reported; the exit code is 4 when some were reviewed and some not.
You can't approve or reject text you wrote yourself while another reviewer exists for the
locale (own_text): ask them. Without one, you may, and the history records a self-approval.`

type translationUnit struct{ key, locale string }

func (u translationUnit) String() string { return u.key + "@" + u.locale }

type translationsArgs struct {
	state string
	units []translationUnit
}

func parseTranslationsArgs(inv *invocation, args []string) (translationsArgs, error) {
	fs := inv.flags(translationsUsage)
	var a translationsArgs
	fs.StringVar(&a.state, "state", "", "the review state to move to: "+strings.Join(reviewTargetStates, ", "))
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 || pos[0] != "review" {
		return a, usageError(inv.name, "missing action: review")
	}
	if !slices.Contains(reviewTargetStates, a.state) {
		return a, &Error{Exit: ExitUsage, Code: "invalid_state", What: fmt.Sprintf("--state must be one of %s, not %q", strings.Join(reviewTargetStates, ", "), a.state),
			Fix: "e.g. glossa translations review checkout.pay@de --state approved"}
	}
	if len(pos) == 1 {
		return a, usageError(inv.name, "review takes one or more units, as key@locale (checkout.pay@de)")
	}
	for _, ref := range pos[1:] {
		i := strings.LastIndex(ref, "@")
		if i <= 0 || i == len(ref)-1 {
			return a, usageError(inv.name, "%q is not a unit: write it as key@locale (checkout.pay@de)", ref)
		}
		l, err := normalizeLocale(inv, "the unit's locale", ref[i+1:])
		if err != nil {
			return a, err
		}
		u := translationUnit{key: ref[:i], locale: l}
		if !slices.Contains(a.units, u) {
			a.units = append(a.units, u)
		}
	}
	return a, nil
}

// ── running ─────────────────────────────────────────────────────────

func runTranslations(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseTranslationsArgs(inv, args)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	out := translationsReviewDoc{Schema: translationsReviewSchema, State: a.state, Units: []translationReviewJSON{}}
	var firstErr *Error
	for _, u := range a.units {
		res, rerr := inv.reviewUnit(ctx, p, u, a.state)
		if rerr != nil {
			if len(a.units) == 1 {
				return rerr
			}
			if firstErr == nil {
				firstErr = rerr
			}
			res.Error = &reviewErrorJSON{Code: rerr.Code, Message: rerr.What, Why: rerr.Why, Fix: rerr.Fix}
			out.Failed++
		} else {
			out.Reviewed++
		}
		out.Units = append(out.Units, res)
	}
	if err := inv.emit(out, func(pr *printer) { printTranslationsReview(pr, out) }); err != nil {
		return err
	}
	switch {
	case firstErr == nil:
		return nil
	case out.Reviewed > 0:
		return silentExit(ExitPartial, "partial")
	}
	return silentExit(firstErr.Exit, firstErr.Code)
}

func printTranslationsReview(pr *printer, d translationsReviewDoc) {
	for _, u := range d.Units {
		if u.Error != nil {
			pr.line("%s %s@%s: %s", pr.fail(), u.Key, u.Locale, u.Error.Message)
			if u.Error.Why != "" {
				pr.line("  %s %s", pr.dim("why:"), u.Error.Why)
			}
			if u.Error.Fix != "" {
				pr.line("  %s %s", pr.dim("fix:"), u.Error.Fix)
			}
			continue
		}
		pr.line("%s %s@%s: %s → %s %s", pr.pass(), u.Key, u.Locale, u.From, u.State, pr.dim(fmt.Sprintf("(revision %d)", u.Revision)))
		if u.Outdated {
			pr.line("  %s", pr.dim("the source changed since this text was written: the review stands, the text is outdated"))
		}
	}
	if len(d.Units) > 1 {
		pr.line("")
		pr.line("%d reviewed, %d failed", d.Reviewed, d.Failed)
	}
}

// reviewUnit reads the unit, reviews it against what it read and, when
// that is stale (412), reads once more and tries once more.
func (inv *invocation) reviewUnit(ctx context.Context, p *project, u translationUnit, state string) (translationReviewJSON, *Error) {
	res := translationReviewJSON{Key: u.key, Locale: u.locale}
	for attempt := 0; ; attempt++ {
		cur, etag, err := p.client.Translation(ctx, p.scope, u.key, u.locale)
		if err != nil {
			return res, inv.reviewError(err, u, "", state, "read")
		}
		res.From = string(cur.State)
		done, _, err := p.client.ReviewTranslation(ctx, p.scope, u.key, u.locale, etag, state)
		if err == nil {
			res.State, res.Revision, res.Outdated = string(done.State), done.Revision, done.Outdated
			return res, nil
		}
		var ae *remote.APIError
		if errors.As(err, &ae) && ae.Status == 412 && attempt == 0 {
			res.Retried = true
			continue
		}
		return res, inv.reviewError(err, u, res.From, state, "review")
	}
}

// ── errors ──────────────────────────────────────────────────────────

// reviewError explains a failed read or review of a unit, by the
// reviewTranslation operation's problem codes.
func (inv *invocation) reviewError(err error, u translationUnit, from, state, step string) *Error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return asError(err)
	}
	e := asError(inv.apiError(err, fmt.Sprintf("can't %s %s", step, u)))
	switch {
	case ae.Status == 403 && ae.Code == "review_forbidden":
		e.What = fmt.Sprintf("you may not review %s as %s", u, state)
		e.Why = "the server refused it (review_forbidden" + detail(ae) + ")"
		e.Fix = fmt.Sprintf("approving and rejecting need translations.review for %s, the other states translations.write: "+
			"use a credential that has it, or ask an owner", u.locale)
	case ae.Status == 403 && ae.Code == "own_text":
		e.What = fmt.Sprintf("you may not %s %s: you wrote its current text", strings.TrimSuffix(state, "d"), u)
		e.Why = "four-eyes: an author never approves or rejects their own work when someone else could review it (own_text)"
		e.Fix = "another reviewer exists for this locale — ask them; moving it to draft or needs_review is still yours"
	case ae.Status == 409 && ae.Code == "invalid_transition":
		e.What = fmt.Sprintf("%s can't move from %s to %s", u, orDefault(from, "its state"), state)
		e.Why = orDefault(ae.Detail, "the review state machine does not allow it (invalid_transition)")
		e.Fix = fmt.Sprintf("`glossa messages` and `glossa status` show where units stand; choose a state %s can move to", u)
	case ae.Status == 400 && ae.Code == "invalid_state":
		e.Exit = ExitUsage
		e.What = fmt.Sprintf("the server does not know the state %q", state)
		e.Why = orDefault(ae.Detail, "invalid_state")
		e.Fix = "--state is one of " + strings.Join(reviewTargetStates, ", ")
	case ae.Status == 404:
		e.What = fmt.Sprintf("no translation %s", u)
		e.Why = "the project has no translation of that message in that locale (" + orDefault(ae.Code, "not_found") + detail(ae) + ")"
		e.Fix = "check the key and locale: `glossa messages` lists keys, `glossa locales` the project's locales"
	case ae.Status == 412:
		e.Code = "precondition_failed"
		e.What = fmt.Sprintf("%s changed while it was being reviewed", u)
		e.Why = "someone else changed the translation between reading it and reviewing it, twice in a row (412)"
		e.Fix = fmt.Sprintf("run `glossa translations review %s --state %s` again once it has settled", u, state)
	case ae.Status == 428:
		e.Why = "the server wants the review made against a version (428)"
	}
	return e
}
