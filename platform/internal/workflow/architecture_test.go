package workflow_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	localization "go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// The §2.1 architecture test (RFC 0006 §2.1, intent §42).
//
// Intent §42 says no single organisation's localization process may be
// encoded in the core domain model. That sentence is only worth what
// enforces it, so each rule below is a mechanical check over the source
// tree, and each names the rule it holds when it fails. The tests read
// source, not compiled packages, so they also see code behind build tags.

const module = "go.klarlabs.de/glossa/platform"

// platformRoot is platform/, relative to this package's directory.
const platformRoot = "../.."

// theFourReviewStates are the publication contract (RFC 0006 §2.2):
// what Release's environment policies and the publish gate read. A
// process lives in Workflow; it never becomes a fifth state here.
var theFourReviewStates = []string{"approved", "draft", "needs_review", "rejected"}

// guardedContexts are the contexts that must never know a workflow
// exists: Workflow depends on their ports, never the reverse (§2.1).
var guardedContexts = []string{"localization", "release", "intelligence"}

// TestReviewStateHasExactlyFourValues holds §2.1 rule 1 in three ways,
// because a fifth state could arrive by any of them: a new constant, a
// new key in the default flow, or a bare conversion somewhere else in
// the module (`domain.ReviewState("legal_review")`).
func TestReviewStateHasExactlyFourValues(t *testing.T) {
	t.Run("constants", func(t *testing.T) {
		got := reviewStateConstants(t)
		if !slices.Equal(got, theFourReviewStates) {
			t.Fatalf("localization/domain declares ReviewState values %v, want exactly %v.\n"+
				"RFC 0006 §2.1 rule 1: an organisation's stage (legal review, vendor done, second review) "+
				"is a state of its workflow definition, not of the publication contract. "+
				"Model it in a glossa.workflow/v1 document instead.", got, theFourReviewStates)
		}
	})
	t.Run("default flow", func(t *testing.T) {
		flow := localization.DefaultReviewFlow()
		var got []string
		for from, tos := range flow.Transitions {
			got = append(got, string(from))
			for _, to := range tos {
				if !slices.Contains(theFourReviewStates, string(to)) {
					t.Errorf("DefaultReviewFlow moves %s to %q, which is not one of the four review states (RFC 0006 §2.1 rule 1)", from, to)
				}
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, theFourReviewStates) {
			t.Errorf("DefaultReviewFlow has states %v, want exactly %v (RFC 0006 §2.1 rule 1)", got, theFourReviewStates)
		}
		for _, s := range theFourReviewStates {
			if _, err := localization.ParseReviewState(s); err != nil {
				t.Errorf("ParseReviewState(%q): %v — the four states must all parse", s, err)
			}
		}
	})
	t.Run("no conversions to a fifth", func(t *testing.T) {
		forEachGoFile(t, platformRoot+"/internal", false, func(path string, f *ast.File) {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 || !namesReviewState(call.Fun) {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, _ := strconv.Unquote(lit.Value)
				if !slices.Contains(theFourReviewStates, v) {
					t.Errorf("%s converts %q to a ReviewState: it is not one of the four (RFC 0006 §2.1 rule 1)",
						strings.TrimPrefix(filepath.ToSlash(path), platformRoot+"/"), v)
				}
				return true
			})
		})
	})
}

// TestGuardedContextsDoNotDependOnWorkflow holds §2.1's dependency
// rule: no package of Localization, Release or Intelligence outside its
// adapters reaches internal/workflow, directly or through anything it
// imports. The one thing they ask of Workflow (Assignments.Covers, §3.3)
// is a port they declare and an adapter implements.
func TestGuardedContextsDoNotDependOnWorkflow(t *testing.T) {
	graph := importGraph(t)
	workflowPkg := module + "/internal/workflow"
	for pkg := range graph {
		if !guarded(pkg) {
			continue
		}
		if chain := pathTo(graph, pkg, func(p string) bool {
			return p == workflowPkg || strings.HasPrefix(p, workflowPkg+"/")
		}); chain != nil {
			t.Errorf("%s depends on Workflow: %s\n"+
				"RFC 0006 §2.1: Workflow depends on their ports, never the reverse. Declare a port in this "+
				"context and implement it in an adapter.", short(pkg), strings.Join(shortAll(chain), " → "))
		}
	}
}

// TestGuardedContextsDoNotBranchOnWho holds the part of §2.1 rule 3
// that a machine can check: the core of Localization, Release and
// Intelligence cannot name a role, a member or a vendor, because it does
// not import Identity's model at all. It asks authz whether a permission
// is held — the one question that stays the same for every
// organisation — and never who is asking.
func TestGuardedContextsDoNotBranchOnWho(t *testing.T) {
	graph := importGraph(t)
	identityModel := module + "/internal/identity/domain"
	for pkg, imports := range graph {
		if guarded(pkg) && slices.Contains(imports, identityModel) {
			t.Errorf("%s imports identity/domain. RFC 0006 §2.1 rule 3: the core of Localization, Release and "+
				"Intelligence asks authz whether a permission is held, never which role, member or vendor "+
				"is asking. Route by who in a workflow definition instead.", short(pkg))
		}
	}
}

// TestNoProcessIsCompiledIntoGo holds §2.1 rule 2. statekit's fluent
// builder is how a machine gets compiled into Go; the branch lifecycle
// is the one place that is right, because a Git branch's life is a fact
// about Git and not a choice. Every other machine — the default review
// process included — is a glossa.workflow/v1 document loaded through
// statekit.FromJSON.
func TestNoProcessIsCompiledIntoGo(t *testing.T) {
	allowed := map[string]bool{"internal/catalog/domain/lifecycle.go": true}
	forEachGoFile(t, platformRoot+"/internal", false, func(path string, f *ast.File) {
		rel := strings.TrimPrefix(filepath.ToSlash(path), platformRoot+"/")
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewMachine" {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "statekit" && !allowed[rel] {
				t.Errorf("%s builds a machine with statekit.NewMachine. RFC 0006 §2.1 rule 2: a process is a "+
					"choice, so it is a glossa.workflow/v1 document, never Go.", rel)
			}
			return true
		})
	})
}

// ── source reading ──────────────────────────────────────────────────

// reviewStateConstants returns the values of every constant
// localization/domain declares with type ReviewState, sorted.
func reviewStateConstants(t *testing.T) []string {
	t.Helper()
	var values []string
	forEachGoFile(t, platformRoot+"/internal/localization/domain", true, func(_ string, f *ast.File) {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "ReviewState" {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok {
						t.Fatalf("a ReviewState constant is not a string literal; this test cannot see its value")
					}
					s, _ := strconv.Unquote(lit.Value)
					values = append(values, s)
				}
				if len(vs.Values) == 0 {
					t.Fatalf("a ReviewState constant has no explicit value; this test cannot see it")
				}
			}
		}
	})
	slices.Sort(values)
	return values
}

func namesReviewState(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "ReviewState"
	case *ast.SelectorExpr:
		return f.Sel.Name == "ReviewState"
	}
	return false
}

// forEachGoFile parses every non-test Go file under root (only root
// itself when flat), whatever its build tags.
func forEachGoFile(t *testing.T, root string, flat bool, fn func(path string, f *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (flat || d.Name() == "testdata") {
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
		fn(path, f)
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
}

// importGraph maps every package under platform/internal to the
// module-internal packages its non-test files import.
func importGraph(t *testing.T) map[string][]string {
	t.Helper()
	graph := map[string][]string{}
	fset := token.NewFileSet()
	root := platformRoot + "/internal"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		dir, _ := filepath.Rel(platformRoot, filepath.Dir(path))
		pkg := module + "/" + filepath.ToSlash(dir)
		if _, ok := graph[pkg]; !ok {
			graph[pkg] = nil
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(p, module+"/") && !slices.Contains(graph[pkg], p) {
				graph[pkg] = append(graph[pkg], p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read imports: %v", err)
	}
	if len(graph) == 0 {
		t.Fatal("found no packages; the test is reading the wrong directory")
	}
	if _, err := os.Stat(platformRoot + "/internal/localization/domain"); err != nil {
		t.Fatalf("localization/domain is not where this test looks: %v", err)
	}
	return graph
}

// guarded reports whether pkg is a non-adapter package of Localization,
// Release or Intelligence.
func guarded(pkg string) bool {
	for _, c := range guardedContexts {
		base := module + "/internal/" + c
		if pkg == base || strings.HasPrefix(pkg, base+"/") {
			return !strings.Contains(pkg+"/", "/adapters/")
		}
	}
	return false
}

// pathTo returns an import chain from start to the first package that
// satisfies target, or nil.
func pathTo(graph map[string][]string, start string, target func(string) bool) []string {
	prev := map[string]string{start: ""}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range graph[cur] {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = cur
			if target(next) {
				chain := []string{next}
				for p := cur; p != ""; p = prev[p] {
					chain = append([]string{p}, chain...)
				}
				return chain
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func short(pkg string) string { return strings.TrimPrefix(pkg, module+"/") }

func shortAll(pkgs []string) []string {
	out := make([]string, len(pkgs))
	for i, p := range pkgs {
		out[i] = short(p)
	}
	return out
}
