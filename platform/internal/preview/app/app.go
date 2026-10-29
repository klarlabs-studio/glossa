// Package app is the message preview's application layer: a stateless
// use of the MessageFormat kernel — parse (MF1 or MF2) into the
// canonical model, serialize it as MF2, derive arguments and markup, and
// format it with sample values — for Studio's live preview and parity
// with the CLI's offline checks. RFC 0002 §5 keeps exactly one MF1
// converter, in Go; this is how clients that aren't Go reach it.
//
// It reads no tenant data, so it needs an authenticated caller but no
// permission, and it is rate-limited per caller through the Limiter
// port.
package app

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// Input limits. The source is bounded by mfcontent.MaxTextBytes, like a
// stored message.
const (
	MaxValues       = 100
	MaxValueNameLen = 64
	MaxValueBytes   = 1000
)

// Errors.
var (
	ErrRateLimited   = errors.New("preview: too many previews; slow down")
	ErrInvalidValues = errors.New("preview: invalid values")
)

// Limiter decides whether a caller may preview now. Allow consumes one
// request from key's budget.
type Limiter interface {
	Allow(ctx context.Context, key string) bool
}

// Input is what to preview.
type Input struct {
	Source string
	// Syntax "" means MF1.
	Syntax string
	Locale string
	// Values, when non-nil, are formatted with: strings, numbers
	// (float64, as JSON decodes them) or booleans, by argument name.
	Values map[string]any
	// BidiIsolation nil means the MF2 default (on).
	BidiIsolation *bool
}

// Stages of a preview a problem can come from.
const (
	StageParse  = "parse"
	StageFormat = "format"
)

// Issue is a problem the kernel reported, with its stable code.
type Issue struct {
	Stage   string
	Code    string
	Message string
}

// Result is what the kernel made of the source. Content is zero and MF2
// empty when the source didn't parse; Formatted is set when values were
// given and the message is valid.
type Result struct {
	Content   mfcontent.Content
	MF2       string
	Formatted *string
	Errors    []Issue
}

// Valid reports whether the source parsed into a valid message.
func (r Result) Valid() bool { return !r.Content.IsZero() }

// Service runs previews.
type Service struct{ limiter Limiter }

// New returns the service.
func New(limiter Limiter) *Service { return &Service{limiter: limiter} }

// Preview parses, serializes and (with values) formats a message. Input
// the caller got wrong (size, locale, syntax, values) is an error;
// source the kernel rejects is a Result with parse Errors, so an editor
// can show it inline.
func (s *Service) Preview(ctx context.Context, in Input) (Result, error) {
	p, err := authz.Authenticated(ctx)
	if err != nil {
		return Result{}, err
	}
	if !s.limiter.Allow(ctx, p.Actor.String()) {
		return Result{}, ErrRateLimited
	}
	syntax, err := mfcontent.ParseSyntax(in.Syntax, mfcontent.MF1)
	if err != nil {
		return Result{}, err
	}
	locale, err := bcp47.Parse(in.Locale)
	if err != nil {
		return Result{}, err
	}
	if err := checkValues(in.Values); err != nil {
		return Result{}, err
	}
	content, err := mfcontent.Parse(syntax, in.Source, locale)
	var invalid *mfcontent.InvalidError
	if errors.As(err, &invalid) {
		return Result{Errors: []Issue{{Stage: StageParse, Code: string(invalid.Code), Message: invalid.Message}}}, nil
	}
	if err != nil {
		return Result{}, err
	}
	res := Result{Content: content}
	// A parsed model always serializes; failing to is a kernel bug.
	if res.MF2, err = mf.Stringify(content.Model); err != nil {
		return Result{}, fmt.Errorf("preview: stringify a parsed message: %w", err)
	}
	if in.Values != nil {
		res.Formatted, res.Errors = format(content.Model, locale, in.Values, in.BidiIsolation)
	}
	return res, nil
}

// format formats msg; MF2 formatting always yields a usable string, with
// fallbacks for placeholders that failed, unless the locale or message
// can't be formatted at all.
func format(msg mf.Message, locale bcp47.Tag, values map[string]any, bidi *bool) (*string, []Issue) {
	var opts []mf.FormatOption
	if bidi != nil {
		opts = append(opts, mf.WithBidiIsolation(*bidi))
	}
	out, err := mf.Format(msg, locale.String(), values, opts...)
	var (
		fe     *mf.FormatError
		single *mf.Error
	)
	switch {
	case err == nil:
		return &out, nil
	case errors.As(err, &fe):
		issues := make([]Issue, len(fe.Errors))
		for i, e := range fe.Errors {
			issues[i] = Issue{Stage: StageFormat, Code: string(e.Code), Message: e.Message}
		}
		return &out, issues
	case errors.As(err, &single):
		return nil, []Issue{{Stage: StageFormat, Code: string(single.Code), Message: single.Message}}
	default:
		return nil, []Issue{{Stage: StageFormat, Code: string(mf.CodeInternalError), Message: err.Error()}}
	}
}

// checkValues enforces the value limits: scalar values only, so a
// preview can't smuggle structures into the engine.
func checkValues(values map[string]any) error {
	if len(values) > MaxValues {
		return fmt.Errorf("%w: at most %d values", ErrInvalidValues, MaxValues)
	}
	for name, v := range values {
		if name == "" || utf8.RuneCountInString(name) > MaxValueNameLen {
			return fmt.Errorf("%w: names are 1 to %d characters", ErrInvalidValues, MaxValueNameLen)
		}
		switch v := v.(type) {
		case string:
			if len(v) > MaxValueBytes {
				return fmt.Errorf("%w: %s is longer than %d bytes", ErrInvalidValues, name, MaxValueBytes)
			}
		case float64, bool:
		default:
			return fmt.Errorf("%w: %s must be a string, number or boolean", ErrInvalidValues, name)
		}
	}
	return nil
}
