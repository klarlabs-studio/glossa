package cli

import (
	"errors"
	"fmt"
)

// ExitCode is the process exit status. CI scripts branch on it, so the
// values never change meaning (README: "Exit codes").
type ExitCode int

// Exit codes.
const (
	// ExitOK: the command did what was asked.
	ExitOK ExitCode = 0
	// ExitCheckFailed: a check (glossa check, extract --strict) found
	// problems the policy doesn't accept.
	ExitCheckFailed ExitCode = 1
	// ExitUsage: bad flags or arguments, a missing or invalid glossa.yaml,
	// local files that can't be read, or a command that isn't available.
	ExitUsage ExitCode = 2
	// ExitNetwork: the server couldn't be reached, or refused the request
	// (authentication, permissions, a missing project, a server error).
	ExitNetwork ExitCode = 3
	// ExitPartial: the request went through but some items failed (push,
	// import); the report lists them.
	ExitPartial ExitCode = 4
	// ExitHeld: a publish or promote went through but the environment
	// requires approvals, so it was held as a release request and
	// nothing was deployed (RFC 0006 §5.1). The output names the
	// request and the next step (`glossa approve`).
	ExitHeld ExitCode = 5
)

// Error is a CLI failure that says what happened, where, why, and how to
// fix it (product intent §52). Code is a stable snake_case identifier
// for scripts (`--json` prints it); the prose may change.
type Error struct {
	Exit  ExitCode
	Code  string
	What  string
	Where string
	Why   string
	Fix   string
	// Silent errors only set the exit code: the command already reported
	// (e.g. a failed check printed its findings).
	Silent bool
	Err    error
}

func (e *Error) Error() string {
	msg := e.What
	if e.Why != "" {
		msg += ": " + e.Why
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// jsonError is the `--json` shape of an error.
type jsonError struct {
	Code     string `json:"code"`
	ExitCode int    `json:"exit_code"`
	Message  string `json:"message"`
	Where    string `json:"where,omitempty"`
	Why      string `json:"why,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

func (e *Error) json() jsonError {
	return jsonError{Code: e.Code, ExitCode: int(e.Exit), Message: e.What, Where: e.Where, Why: e.Why, Fix: e.Fix}
}

// usageError is a flag or argument mistake.
func usageError(cmd, format string, args ...any) *Error {
	return &Error{
		Exit: ExitUsage, Code: "invalid_usage",
		What: fmt.Sprintf(format, args...),
		Fix:  fmt.Sprintf("run `glossa %s --help` for the flags it takes", cmd),
	}
}

// silentExit ends the command with code without printing anything more.
func silentExit(code ExitCode, errCode string) *Error {
	return &Error{Exit: code, Code: errCode, Silent: true}
}

// asError turns any error into an *Error; unknown ones are internal
// failures reported as usage errors (exit 2), since they're usually local.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Exit: ExitUsage, Code: "failed", What: err.Error(), Err: err}
}
