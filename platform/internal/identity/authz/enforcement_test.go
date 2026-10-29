package authz_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// projectAware are the authz checks that consult a principal's project
// scope or assignment visibility (RFC 0006 §3.3, §4.1).
var projectAware = []string{
	"RequireIn", "RequireForIn", "RequireUnit", "RequireMessage", "RequireProject", "RequireLocaleIn",
	"Visible", "InProject", "Projects", "RequireUnscoped", "RequireInEnvironment", "RequireRow",
}

// appMethods parses one context's application layer and reports, for
// every exported method of its application services, whether it reaches a
// project-aware authz check — directly or through the package's own
// helpers, transitively.
func appMethods(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	type fn struct {
		exported bool
		// service is an exported method of a type named …Service.
		service bool
		direct  bool
		// checks is a direct call of any authz.Require… check.
		checks bool
		calls  []string
	}
	funcs := map[string]*fn{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				d, ok := decl.(*ast.FuncDecl)
				if !ok || d.Body == nil {
					continue
				}
				recv, recvName := "", ""
				if d.Recv != nil && len(d.Recv.List) == 1 {
					recv = typeName(d.Recv.List[0].Type)
					if len(d.Recv.List[0].Names) == 1 {
						recvName = d.Recv.List[0].Names[0].Name
					}
				}
				key := d.Name.Name
				if recv != "" {
					key = recv + "." + d.Name.Name
				}
				// A use case is an exported method of an application
				// service — or of any other type whose method checks a
				// permission (Workflow's Instances read side was one,
				// and the …Service rule alone let it check workflows.read
				// without the project). Constructors, options, metrics
				// and values are not use cases.
				f := &fn{
					exported: d.Name.IsExported() && recv != "",
					service:  strings.HasSuffix(recv, "Service"),
				}
				ast.Inspect(d.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch c := call.Fun.(type) {
					case *ast.Ident:
						f.calls = append(f.calls, c.Name)
					case *ast.SelectorExpr:
						if x, ok := c.X.(*ast.Ident); ok {
							switch {
							case x.Name == "authz" && slices.Contains(projectAware, c.Sel.Name):
								f.direct = true
								f.checks = true
							case x.Name == "authz" && strings.HasPrefix(c.Sel.Name, "Require"):
								f.checks = true
							case x.Name == recvName && recv != "":
								f.calls = append(f.calls, recv+"."+c.Sel.Name)
							}
						}
					}
					return true
				})
				funcs[key] = f
			}
		}
	}
	// reach reports whether k, or anything it calls in its package,
	// has the property has.
	reach := func(has func(*fn) bool) func(string) bool {
		memo := map[string]bool{}
		var visit func(string, map[string]bool) bool
		visit = func(k string, seen map[string]bool) bool {
			if r, ok := memo[k]; ok {
				return r
			}
			f, ok := funcs[k]
			if !ok || seen[k] {
				return false
			}
			seen[k] = true
			r := has(f)
			for _, c := range f.calls {
				if r {
					break
				}
				r = visit(c, seen)
			}
			memo[k] = r
			return r
		}
		return func(k string) bool { return visit(k, map[string]bool{}) }
	}
	reachesProject := reach(func(f *fn) bool { return f.direct })
	checksAPermission := reach(func(f *fn) bool { return f.checks })
	out := map[string]bool{}
	for k, f := range funcs {
		if f.exported && (f.service || checksAPermission(k)) {
			out[k] = reachesProject(k)
		}
	}
	return out
}

func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return typeName(t.X)
	}
	return ""
}

// TestEveryUseCaseHasAnEnforcementDecision enumerates every exported
// method of every bounded context's application layer — generated from
// the source, not remembered — and fails for one that neither reaches a
// project-aware authz check nor has a recorded reason not to
// (notProjectAddressed). It is the application-layer twin of
// cmd/glossa-server's TestEveryOperationHasARestrictionDecision, which
// does the same for every operation in the OpenAPI document: together
// they make a use case or an endpoint nobody decided about a failing
// test, not a hole (RFC 0006 §11.1 rule 4). A recorded reason that no
// longer holds — the method enforces now, or is gone — fails too.
func TestEveryUseCaseHasAnEnforcementDecision(t *testing.T) {
	root := filepath.Join("..", "..")
	contexts, err := filepath.Glob(filepath.Join(root, "*", "app"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, dir := range contexts {
		ctxName := filepath.Base(filepath.Dir(dir))
		for method, enforces := range appMethods(t, dir) {
			key := ctxName + "." + method
			seen[key] = true
			reason, exempt := notProjectAddressed[key]
			switch {
			case enforces && exempt:
				t.Errorf("%s reaches a project-aware check; drop its recorded exemption (%q)", key, reason)
			case !enforces && !exempt:
				t.Errorf("%s reaches no project-aware authz check and has no recorded reason not to: "+
					"check the project (authz.RequireIn, InProject, Visible, …) or record why it is not project-addressed", key)
			}
		}
	}
	for key := range notProjectAddressed {
		if !seen[key] {
			t.Errorf("an exemption is recorded for %s, which no longer exists", key)
		}
	}
}
