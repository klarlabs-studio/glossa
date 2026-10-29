package domain

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Problem is one reason a catalog can't be released: something an
// artifact or manifest can't carry (runtimes/SPEC.md §1).
type Problem struct {
	// Key is the message key when one message is the cause.
	Key string
	// Locale is the locale when one locale (or one translation) is the
	// cause.
	Locale string
	Detail string
}

// NotReleasableError lists every problem a build found, so a dry run
// can report them all at once. It is ErrNotReleasable.
type NotReleasableError struct {
	Problems []Problem
}

// maxProblemsInMessage bounds Error's text; Problems holds them all.
const maxProblemsInMessage = 10

func (e *NotReleasableError) Error() string {
	details := make([]string, 0, min(len(e.Problems), maxProblemsInMessage))
	for i, p := range e.Problems {
		if i == maxProblemsInMessage {
			break
		}
		details = append(details, p.Detail)
	}
	msg := ErrNotReleasable.Error() + ": " + strings.Join(details, "; ")
	if more := len(e.Problems) - len(details); more > 0 {
		msg += fmt.Sprintf("; and %d more", more)
	}
	return msg
}

// Unwrap makes the error ErrNotReleasable.
func (e *NotReleasableError) Unwrap() error { return ErrNotReleasable }

// problems collects a build's problems.
type problems []Problem

func (ps *problems) add(key, locale, format string, args ...any) {
	*ps = append(*ps, Problem{Key: key, Locale: locale, Detail: fmt.Sprintf(format, args...)})
}

// err returns nil, or the problems in a stable order.
func (ps problems) err() error {
	if len(ps) == 0 {
		return nil
	}
	out := slices.Clone([]Problem(ps))
	slices.SortStableFunc(out, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.Locale, b.Locale))
	})
	return &NotReleasableError{Problems: out}
}
