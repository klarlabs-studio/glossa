package outbox_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The event registry, read from the code itself (RFC 0006 §6.1, §11.1
// rule 4): completeness comes from the source, not from a list someone
// has to remember to extend. Every event reaches the outbox through an
// outbox.Event composite literal — nothing else constructs one — so if
// every such literal names an actor, every event type does. A context
// that adds an event without one fails here, before Publish would
// refuse it at run time.

const outboxImport = "go.klarlabs.de/glossa/platform/internal/kernel/outbox"

// platformRoot is platform/, seen from this package's directory.
const platformRoot = "../../.."

var eventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,2}$`)

type eventLiteral struct {
	pos   string
	typ   string // the Type field as written
	actor string // the Actor field as written; "" when missing
}

type registry struct {
	types    map[string]string // event type → where its constant is declared
	literals []eventLiteral
	other    []string // outbox.Event values made some other way
}

func scanPlatform(t *testing.T) registry {
	t.Helper()
	reg := registry{types: map[string]string{}}
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
		reg.scanFile(fset, f)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", platformRoot, err)
	}
	return reg
}

func (r *registry) scanFile(fset *token.FileSet, f *ast.File) {
	alias := outboxAlias(f)
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ValueSpec:
			r.declaredTypes(fset, n)
			if alias != "" && n.Type != nil && isEvent(n.Type, alias) {
				r.other = append(r.other, fset.Position(n.Pos()).String()+": var of outbox.Event")
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 && alias != "" && isEvent(n.Args[0], alias) {
				r.other = append(r.other, fset.Position(n.Pos()).String()+": new(outbox.Event)")
			}
		case *ast.CompositeLit:
			if alias != "" && n.Type != nil && isEvent(n.Type, alias) && len(n.Elts) > 0 {
				r.literals = append(r.literals, literalOf(fset, n))
			}
		}
		return true
	})
}

// declaredTypes records `EventX = "context.aggregate.verb"` constants.
func (r *registry) declaredTypes(fset *token.FileSet, spec *ast.ValueSpec) {
	for i, name := range spec.Names {
		if !strings.HasPrefix(name.Name, "Event") || i >= len(spec.Values) {
			continue
		}
		lit, ok := spec.Values[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		v, err := strconv.Unquote(lit.Value)
		if err == nil && eventTypePattern.MatchString(v) {
			r.types[v] = fset.Position(name.Pos()).String()
		}
	}
}

func outboxAlias(f *ast.File) string {
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == outboxImport {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "outbox"
		}
	}
	return ""
}

func isEvent(e ast.Expr, alias string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Event" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == alias
}

func literalOf(fset *token.FileSet, lit *ast.CompositeLit) eventLiteral {
	out := eventLiteral{pos: fset.Position(lit.Pos()).String()}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Type":
			out.typ = exprString(kv.Value)
		case "Actor":
			out.actor = exprString(kv.Value)
		}
	}
	return out
}

func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = exprString(a)
		}
		return exprString(e.Fun) + "(" + strings.Join(args, ", ") + ")"
	}
	return "<expr>"
}

// noActor are the ways of writing an Actor field that name no one.
func noActor(written string) bool {
	switch written {
	case "", `""`, "outbox.ActorUnknown", `outbox.Actor("")`, `outbox.Actor(outbox.ActorUnknown)`:
		return true
	}
	return false
}

func TestEveryEventTypeNamesItsActor(t *testing.T) {
	reg := scanPlatform(t)

	// The scan must see the codebase, or every assertion below is vacuous.
	for _, known := range []string{
		"identity.member.added", "catalog.message.source_revised", "localization.translation.revised",
		"release.published", "knowledge.concept.created", "quality.check_run.recorded",
		"context.build.ingested", "integration.import.completed",
	} {
		if _, ok := reg.types[known]; !ok {
			t.Errorf("the registry scan did not find %s: is it reading platform/?", known)
		}
	}
	if len(reg.literals) < 20 {
		t.Fatalf("found only %d outbox.Event literals: the scan is not reading the contexts", len(reg.literals))
	}

	types := make([]string, 0, len(reg.types))
	for typ := range reg.types {
		types = append(types, typ)
	}
	slices.Sort(types)
	for _, typ := range types {
		t.Logf("event type %-45s declared at %s", typ, reg.types[typ])
	}

	for _, lit := range reg.literals {
		if noActor(lit.actor) {
			t.Errorf("%s: outbox.Event{Type: %s} names no actor — pass the person, token or system that caused it", lit.pos, lit.typ)
		}
	}
	for _, o := range reg.other {
		t.Errorf("%s — build events as outbox.Event literals naming their actor, so this test can see them", o)
	}
}
