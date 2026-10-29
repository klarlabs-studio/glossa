// Package apiconv holds what every context's HTTP adapter needs to
// render the shared message value objects and read concurrency headers
// the same way: message content with derived metadata, QA findings,
// ETags and If-Match.
package apiconv

import (
	"encoding/json"
	"net/http"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/kernel/etag"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// Model renders a canonical model as the MF2 data model JSON object.
func Model(c mfcontent.Content) apiv1.MF2Message {
	m := apiv1.MF2Message{}
	// ModelJSON is always a JSON object.
	_ = json.Unmarshal(c.ModelJSON(), &m)
	return m
}

// Content renders message content with its derived metadata.
func Content(c mfcontent.Content) apiv1.MessageContent {
	out := apiv1.MessageContent{
		Text: c.Text, Syntax: apiv1.Syntax(c.Syntax), Model: Model(c),
		Arguments: make([]apiv1.Argument, len(c.Arguments)), Markup: make([]apiv1.MarkupElement, len(c.Markup)),
	}
	for i, a := range c.Arguments {
		arg := apiv1.Argument{Name: a.Name, Type: apiv1.ArgumentType(a.Type)}
		if a.Function != "" {
			arg.Function = Ptr(a.Function)
		}
		if a.Selector != nil {
			keys := a.Selector.Keys
			if keys == nil {
				keys = []string{}
			}
			arg.Selector = &struct {
				Keys []string                   `json:"keys"`
				Kind apiv1.ArgumentSelectorKind `json:"kind"`
			}{Keys: keys, Kind: apiv1.ArgumentSelectorKind(a.Selector.Kind)}
		}
		out.Arguments[i] = arg
	}
	for i, m := range c.Markup {
		out.Markup[i] = apiv1.MarkupElement{Name: m.Name, Kind: apiv1.MarkupElementKind(m.Kind)}
	}
	return out
}

// Findings renders structural QA findings.
func Findings(fs []mf.Finding) []apiv1.QAFinding {
	out := make([]apiv1.QAFinding, len(fs))
	for i, f := range fs {
		q := apiv1.QAFinding{Code: string(f.Code), Severity: apiv1.QAFindingSeverity(f.Severity), Message: f.Message}
		if f.Subject != "" {
			q.Subject = Ptr(f.Subject)
		}
		if f.Detail != "" {
			q.Detail = Ptr(f.Detail)
		}
		out[i] = q
	}
	return out
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// ETag renders a version as a strong entity tag.
func ETag(version int) *string { return Ptr(etag.Format(version)) }

var errStaleTag = problem.New(http.StatusPreconditionFailed, problem.CodePreconditionFailed,
	"If-Match must be an ETag this API issued")

// IfMatch reads a required If-Match header.
func IfMatch(s string) (int, error) {
	v, ok := etag.Parse(s)
	if !ok {
		return 0, errStaleTag
	}
	return v, nil
}

// OptionalIfMatch reads an optional If-Match header.
func OptionalIfMatch(s *string) (*int, error) {
	if s == nil {
		return nil, nil
	}
	v, err := IfMatch(*s)
	return &v, err
}

// OptionalSingletonIfMatch reads the optional If-Match of a singleton
// that exists with defaults before it is saved: `"0"`, its ETag until
// then, asks for the write only while it is still unsaved.
func OptionalSingletonIfMatch(s *string) (*int, error) {
	if s == nil {
		return nil, nil
	}
	v, ok := etag.ParseUnsaved(*s)
	if !ok {
		return nil, errStaleTag
	}
	return &v, nil
}
