// Package apiv1 is the Go side of the /v1 contract: types and a strict
// net/http server generated from api/openapi.yaml by oapi-codegen (the
// version is pinned by the tool directive in go.mod). Never edit
// apiv1.gen.go; change the spec and regenerate:
//
//	go generate ./internal/apiv1/...
//
// TestGeneratedCodeIsCurrent fails when the committed code is stale.
//
// Each bounded context implements its operations on its own handler
// type; the composition root embeds them into one StrictServerInterface.
package apiv1

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../api/openapi.yaml
