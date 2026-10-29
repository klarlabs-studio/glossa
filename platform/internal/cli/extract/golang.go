package extract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// runtimeImport is the Go runtime; its package name (glossa) isn't its
// path's last element.
const runtimeImport = "go.klarlabs.de/glossa/runtimes/go"

// scanGo parses a Go file and reports its `.T(…)` calls and typed
// accessor calls. goNames maps accessor method names to keys.
func scanGo(file string, src []byte, goNames map[string]string) ([]Usage, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	s := &goScan{file: file, lines: newLines(src), fset: fset, imports: importNames(f), goNames: goNames}
	for _, decl := range f.Decls {
		component := ""
		if fn, ok := decl.(*ast.FuncDecl); ok {
			component = f.Name.Name + "." + funcName(fn)
		}
		s.decl(decl, component)
	}
	return s.out, nil
}

type goScan struct {
	file    string
	lines   *lines
	fset    *token.FileSet
	imports map[string]bool
	goNames map[string]string
	out     []Usage
}

// decl reports the usages in one top-level declaration. Function
// literals belong to it; package-level initializers have no component.
func (s *goScan) decl(decl ast.Decl, component string) {
	locals := declaredNames(decl)
	ast.Inspect(decl, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || s.onPackage(sel.X, locals) {
			return true
		}
		if sel.Sel.Name == "T" {
			if lit := tKey(call.Args); lit != nil {
				s.literal(lit, component)
				return true
			}
		}
		if key, ok := s.goNames[sel.Sel.Name]; ok {
			s.add(key, sel.Sel.Pos(), component, KindAccessor)
		}
		return true
	})
}

// tKey is the key literal of a `.T` call: its first argument if that's a
// string literal, else its second (Client.T(ctx, "…")).
func tKey(args []ast.Expr) *ast.BasicLit {
	for i, a := range args {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return lit
		}
		if i == 1 {
			break
		}
	}
	return nil
}

func (s *goScan) literal(lit *ast.BasicLit, component string) {
	key, err := strconv.Unquote(lit.Value)
	if err != nil || !validKey(key) {
		return
	}
	s.add(key, lit.Pos()+1, component, KindT) // +1: past the opening quote
}

func (s *goScan) add(key string, pos token.Pos, component, kind string) {
	u := s.lines.usage(key, s.file, s.fset.Position(pos).Offset, kind)
	u.Component = component
	s.out = append(s.out, u)
}

// onPackage reports whether x names an imported package (strings.Title),
// not a value: an import's name that no declaration in scope shadows.
func (s *goScan) onPackage(x ast.Expr, locals map[string]bool) bool {
	id, ok := x.(*ast.Ident)
	return ok && s.imports[id.Name] && !locals[id.Name]
}

// funcName is Func, Type.Method or (*Type).Method.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	star := false
	if p, ok := t.(*ast.StarExpr); ok {
		star, t = true, p.X
	}
	// Generic receivers (Set[T]) are named without their parameters.
	switch g := t.(type) {
	case *ast.IndexExpr:
		t = g.X
	case *ast.IndexListExpr:
		t = g.X
	}
	name := "?"
	if id, ok := t.(*ast.Ident); ok {
		name = id.Name
	}
	if star {
		return "(*" + name + ")." + fn.Name.Name
	}
	return name + "." + fn.Name.Name
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// importNames are the names a file's imports bind. Without an explicit
// name the package name is guessed from the path, the usual convention
// (gopkg.in/yaml.v3 → yaml, go-chi/chi/v5 → chi).
func importNames(f *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if spec.Name != nil {
			out[spec.Name.Name] = true
			continue
		}
		if p == runtimeImport {
			out["glossa"] = true
			continue
		}
		elems := strings.Split(p, "/")
		last := elems[len(elems)-1]
		if majorVersion.MatchString(last) && len(elems) > 1 {
			last = elems[len(elems)-2]
		}
		out[last] = true
		if i := strings.Index(last, ".v"); i > 0 {
			last = last[:i]
		}
		last = strings.TrimSuffix(strings.TrimPrefix(last, "go-"), "-go")
		out[strings.ReplaceAll(last, "-", "")] = true
	}
	return out
}

// declaredNames are the names a declaration binds anywhere inside it:
// receivers, parameters and results (of function literals too), :=, var
// and range variables. Scopes aren't tracked, so a name declared anywhere
// in the declaration shadows an import throughout it.
func declaredNames(decl ast.Decl) map[string]bool {
	out := map[string]bool{}
	addIdent := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			out[id.Name] = true
		}
	}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	if fn, ok := decl.(*ast.FuncDecl); ok {
		addFields(fn.Recv)
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncType:
			addFields(n.Params)
			addFields(n.Results)
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, e := range n.Lhs {
					addIdent(e)
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				addIdent(n.Key)
				addIdent(n.Value)
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}
