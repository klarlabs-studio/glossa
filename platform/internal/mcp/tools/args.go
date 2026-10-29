package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/mcp/app"
)

// Page bounds. Every list tool takes `limit` and refuses more than
// MaxLimit; a tool whose own source caps lower says so in its
// description.
//
// The default is deliberately smaller than the REST API's page size
// (50): a tool result is read by a model, and twenty rows it can act on
// beat a hundred it has to skim.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// MaxTextBytes bounds the free text an argument may carry — a TM query,
// a text to recognize terms in. It is the size of a long message, not
// of a document: these tools answer questions about messages.
const MaxTextBytes = 4000

// decode reads a tool's arguments, refusing anything the schema does
// not declare. A client that sends an argument the tool does not know
// is told so, rather than silently getting the answer to a different
// question.
func decode(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &app.InvalidArgumentError{Argument: "arguments", Reason: "not the object this tool declares"}
	}
	return nil
}

// invalid returns the error a tool answers for an argument it cannot
// use. It names the argument, never its value (RFC 0005 §11).
func invalid(argument, reason string) error {
	return &app.InvalidArgumentError{Argument: argument, Reason: reason}
}

// projectOf parses the `project` argument. A well-formed id of another
// tenant is not rejected here: it is passed on, and row-level security
// turns it into a not-found (see the package doc).
func projectOf(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, invalid("project", "not a project id")
	}
	return id, nil
}

// optionalID parses an optional uuid argument; "" is uuid.Nil.
func optionalID(name, s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, invalid(name, "not an id")
	}
	return id, nil
}

// limitOf validates a page size: absent means DefaultLimit, and more
// than cap is refused rather than silently clamped, so a caller never
// believes it saw everything.
func limitOf(n *int, maximum int) (int, error) {
	if n == nil {
		if DefaultLimit > maximum {
			return maximum, nil
		}
		return DefaultLimit, nil
	}
	if *n < 1 || *n > maximum {
		return 0, invalid("limit", fmt.Sprintf("must be 1 to %d", maximum))
	}
	return *n, nil
}

// required rejects an empty string argument.
func required(name, s string) error {
	if s == "" {
		return invalid(name, "is required")
	}
	return nil
}

// boundedText rejects free text larger than MaxTextBytes.
func boundedText(name, s string) error {
	if err := required(name, s); err != nil {
		return err
	}
	if len(s) > MaxTextBytes {
		return invalid(name, fmt.Sprintf("must be at most %d bytes", MaxTextBytes))
	}
	return nil
}

// plural renders "1 thing" / "3 things" for an explanation.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
