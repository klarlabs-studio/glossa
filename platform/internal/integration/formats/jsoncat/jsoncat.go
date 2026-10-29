// Package jsoncat reads and writes JSON message catalogs: one file per
// locale mapping keys to messages, flat ({"checkout.title": "…"}) or
// nested ({"checkout": {"title": "…"}}).
//
// Messages are ICU MessageFormat (MF1) by default, parsed by the
// MessageFormat kernel's one MF1 converter; MF2 syntax is an option.
// Nested objects join their keys with "."; reading accepts flat, nested
// and mixed files. Keys that collide once joined, duplicate keys and
// values that are not strings or objects are errors with a line and
// column.
//
// Writing orders keys lexicographically (nested: at every level), so
// equal catalogs give byte-identical files. An MF1 file needs each
// message's MF1 text: the authored text when it was written in MF1, else
// (for text with plain {$var} placeholders only) a rendering the MF1
// converter verifies to parse back to the same model. A message without
// either fails the write; write MF2 instead.
package jsoncat

import (
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// Layout is the shape of a catalog file.
type Layout string

// Layouts.
const (
	Flat   Layout = "flat"
	Nested Layout = "nested"
)

const format = "json"

// ErrNoMF1 means a message has no MF1 rendering.
var ErrNoMF1 = formats.Unsupportedf("message has no ICU MessageFormat text (authored in MF2); write the catalog as MF2")
