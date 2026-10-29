package cli

import (
	"errors"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Shared by the Quality commands (findings, waive, policy, status
// --quality; RFC 0005 §13 wave 6).

// qualityFixes explains the Quality API's problem codes. Each one says
// what to do, because a CLI that repeats the server's noun and stops is
// a CLI that made the person open the RFC.
var qualityFixes = map[string]string{
	"invalid_waiver":       "a waiver needs a fingerprint and a non-empty reason; --expires must be in the future",
	"invalid_fingerprint":  "pass a fingerprint `glossa findings` printed, e.g. f_7c1a3e9b40d2f815",
	"invalid_check_policy": "fix the policy document; `glossa policy show` prints the one in force",
	"invalid_severity":     "a rule's severity is error, warning or off; fail_on is error, warning or never",
	"unknown_layer":        "a rule selects one of the ten layers; `glossa check --help` lists them",
	"advisory_layer":       "the linguistic layer may not be raised to error: a build never fails on a model's opinion",
	"unknown_locale":       "require_complete may only name locales the project has (`glossa locales` lists them)",
	"invalid_environment":  "an environment block takes require_complete and require_review: approved",
	"waiver_not_found":     "check the ID: `glossa waive --list` shows the project's waivers",
	"check_run_not_found":  "check --run; `glossa findings` without it reads the newest run",
}

// qualityError explains a failed Quality request: invalid input is a
// usage error (exit 2), the server refusing the operation a network one
// (exit 3), with the server's own detail as the reason.
func (inv *invocation) qualityError(err error, what string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if fix, ok := qualityFixes[ae.Code]; ok {
		e.Fix = fix
	}
	switch {
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
	case ae.Status == 404 && ae.Code != "not_found":
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
	case ae.Status == 403:
		e.Fix = "use a token with the needed scope, created in Studio: read for findings and the policy, " +
			"write for waivers and for saving a policy"
	}
	return e
}
