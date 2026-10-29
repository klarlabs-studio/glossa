package domain_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
)

// The audit side of the event registry (RFC 0006 §6.1, §11.1 rule 4):
// every event type declared in the code has a projection, so a new
// event cannot silently skip the audit trail. Like the outbox's own
// registry test (internal/kernel/outbox/event_registry_test.go), it
// reads the source rather than a list someone has to remember: an
// event type is an `Event… = "<context>.<…>"` string constant whose
// first segment names a bounded context — a directory under
// platform/internal with a domain or app package. That excludes the
// Workflow vocabulary's chart events ("translation.revised",
// "timer.due"), which name what a chart reacts to, not what the outbox
// carries.

// platformRoot is platform/, seen from this package's directory.
const platformRoot = "../../.."

var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,2}$`)

func boundedContexts(t *testing.T) []string {
	t.Helper()
	dirs, err := os.ReadDir(filepath.Join(platformRoot, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		for _, layer := range []string{"domain", "app"} {
			if fi, err := os.Stat(filepath.Join(platformRoot, "internal", d.Name(), layer)); err == nil && fi.IsDir() {
				out = append(out, d.Name())
				break
			}
		}
	}
	return out
}

// declaredEventTypes maps each declared event type to where it is.
func declaredEventTypes(t *testing.T) map[string]string {
	t.Helper()
	contexts := boundedContexts(t)
	types := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(platformRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if !strings.HasPrefix(name.Name, "Event") || i >= len(spec.Values) {
					continue
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil || !eventTypePattern.MatchString(v) {
					continue
				}
				first, _, _ := strings.Cut(v, ".")
				if slices.Contains(contexts, first) {
					types[v] = fset.Position(name.Pos()).String()
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", platformRoot, err)
	}
	return types
}

func TestEveryEventTypeHasAProjection(t *testing.T) {
	declared := declaredEventTypes(t)

	// The scan must see the codebase, or the assertions are vacuous.
	for _, known := range []string{
		"identity.member.added", "catalog.message.source_revised", "localization.translation.revised",
		"release.published", "knowledge.concept.created", "quality.check_run.recorded",
		"context.build.ingested", "integration.import.completed",
	} {
		if _, ok := declared[known]; !ok {
			t.Errorf("the scan did not find %s: is it reading platform/?", known)
		}
	}
	if len(declared) < 60 {
		t.Fatalf("found only %d event types: the scan is not reading the contexts", len(declared))
	}
	for _, chart := range []string{"translation.revised", "timer.due", "approval.granted"} {
		if _, ok := declared[chart]; ok {
			t.Errorf("%s is a workflow chart event, not an outbox event type", chart)
		}
	}

	for typ, at := range declared {
		if _, ok := domain.Projections[typ]; !ok {
			t.Errorf("%s (declared at %s) has no audit projection: add it to domain.Projections, "+
				"naming its project and locale paths and the payload fields that are identifiers or selectors "+
				"(RFC 0006 §6.1). An event without one never reaches the audit trail.", typ, at)
		}
	}
	for typ := range domain.Projections {
		if _, ok := declared[typ]; !ok {
			t.Errorf("domain.Projections names %s, which no code declares: remove it", typ)
		}
	}
}

// textFields are payload names that carry content. No projection may
// keep one verbatim, whatever event it is for (RFC 0006 §6.1, §9.5).
var textFields = []string{
	"text", "source", "source_text", "value", "translation", "message_text", "body", "content",
	"email", "description", "comment", "note", "title", "display_name", "password", "secret", "token",
}

func TestNoProjectionKeepsTextVerbatim(t *testing.T) {
	for typ, p := range domain.Projections {
		for _, s := range p.Selectors {
			last := s[strings.LastIndexByte(s, '.')+1:]
			if slices.Contains(textFields, last) {
				// context.build.ingested's "source" is how a build was
				// made ("plugin", "extract", "runtime"), an enum, not text.
				if typ == "context.build.ingested" && s == "source" {
					continue
				}
				t.Errorf("%s keeps %q verbatim: it carries content; record its shape", typ, s)
			}
		}
		for _, path := range []string{p.Project, p.Locale, p.By} {
			if path != "" && !slices.Contains(p.Selectors, path) {
				t.Errorf("%s reads %q into a column but does not list it as a selector", typ, path)
			}
		}
	}
}
