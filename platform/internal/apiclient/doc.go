// Package apiclient is a Go client for the /v1 contract, generated from
// api/openapi.yaml by oapi-codegen (the version is pinned by the tool
// directive in go.mod). The glossa CLI uses it. It comes from the same
// spec as the server (internal/apiv1), so the two can't drift apart.
// Never edit apiclient.gen.go; change the spec and regenerate:
//
//	go generate ./internal/apiclient/...
//
// TestGeneratedCodeIsCurrent fails when the committed code is stale.
package apiclient

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../api/openapi.yaml
