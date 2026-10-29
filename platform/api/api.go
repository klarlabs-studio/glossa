// Package api holds the /v1 contract, api/openapi.yaml: the source of
// truth for glossa-server's REST API (RFC 0002 §2, principle 4). The Go
// server is generated from it into internal/apiv1; a TypeScript client
// for Studio will be generated from it too.
//
// openapi_test.go validates the document and enforces the API
// conventions its description lays out, so a new operation that skips
// one (no security, no pagination, no idempotency key, …) fails CI.
package api

import _ "embed"

// Spec is the OpenAPI 3.1 document.
//
//go:embed openapi.yaml
var Spec []byte
