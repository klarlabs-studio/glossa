package domain_test

import (
	"errors"
	"slices"
	"testing"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

func TestParseToolset(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    domain.Toolset
		wantErr bool
	}{
		{name: "nothing asked for is a read session", in: "", want: domain.ToolsetRead},
		{name: "read", in: "read", want: domain.ToolsetRead},
		{name: "write", in: "write", want: domain.ToolsetWrite},
		{name: "publish", in: "publish", want: domain.ToolsetPublish},
		{name: "admin is not a toolset", in: "admin", wantErr: true},
		{name: "unknown", in: "everything", wantErr: true},
		{name: "case matters", in: "Write", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ParseToolset(tc.in)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrInvalidToolset) {
					t.Fatalf("err = %v, want ErrInvalidToolset", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Errorf("toolset = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToolsetIncludes(t *testing.T) {
	tests := []struct {
		session, tool domain.Toolset
		want          bool
	}{
		{domain.ToolsetRead, domain.ToolsetRead, true},
		{domain.ToolsetRead, domain.ToolsetWrite, false},
		{domain.ToolsetWrite, domain.ToolsetRead, true},
		{domain.ToolsetWrite, domain.ToolsetWrite, true},
		// Publish is not a wider write, it is a different one: a write
		// session cannot move a release and a publish session cannot
		// rewrite the catalog. The toolset is the client's declaration of
		// what the session is *for*, and widening it by accident is the
		// thing the second lock exists to prevent (RFC 0005 §7.2).
		{domain.ToolsetWrite, domain.ToolsetPublish, false},
		{domain.ToolsetPublish, domain.ToolsetRead, true},
		{domain.ToolsetPublish, domain.ToolsetPublish, true},
		{domain.ToolsetPublish, domain.ToolsetWrite, false},
		{domain.ToolsetRead, domain.ToolsetPublish, false},
	}
	for _, tc := range tests {
		if got := tc.session.Includes(tc.tool); got != tc.want {
			t.Errorf("%s session includes a %s tool = %t, want %t", tc.session, tc.tool, got, tc.want)
		}
	}
}

// A toolset never asks for admin: MCP exposes no member, token,
// connection or tenant management (RFC 0005 §7.2).
func TestToolsetScopeIsNeverAdmin(t *testing.T) {
	want := map[domain.Toolset]identity.Scope{
		domain.ToolsetRead:    identity.ScopeRead,
		domain.ToolsetWrite:   identity.ScopeWrite,
		domain.ToolsetPublish: identity.ScopePublish,
	}
	for _, ts := range domain.Toolsets() {
		if s := ts.Scope(); s == identity.ScopeAdmin {
			t.Errorf("%s asks for scope %q", ts, s)
		}
		if got := ts.Scope(); got != want[ts] {
			t.Errorf("%s toolset scope = %q, want %q", ts, got, want[ts])
		}
	}
}

// Toolsets is the ledger's and the metrics' vocabulary as much as the
// transport's: a value added here without widening mcp_tool_calls'
// CHECK would lose an audit row rather than a tool call.
func TestToolsetsAreTheThreeOfM4(t *testing.T) {
	got, want := domain.Toolsets(), []domain.Toolset{domain.ToolsetRead, domain.ToolsetWrite, domain.ToolsetPublish}
	if !slices.Equal(got, want) {
		t.Fatalf("toolsets = %v, want %v", got, want)
	}
}
