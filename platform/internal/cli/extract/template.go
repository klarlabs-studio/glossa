package extract

import (
	"slices"
	"text/template/parse"
)

// templateFuncs are the Go runtime's message functions (Localizer.FuncMap).
var templateFuncs = map[string]bool{"t": true, "td": true, "th": true}

// scanTemplate parses a Go template (text/template or html/template, the
// default delimiters) and reports the first argument of every t, td and
// th call that is a string literal. The functions the template calls
// needn't be known.
func scanTemplate(file string, src []byte) ([]Usage, error) {
	text := string(src)
	tree := parse.New(file)
	tree.Mode = parse.SkipFuncCheck
	trees := map[string]*parse.Tree{}
	if _, err := tree.Parse(text, "", "", trees); err != nil {
		return nil, err
	}
	s := &templateScan{file: file, lines: newLines(src)}
	// Every {{define}} and {{block}} is a tree of its own; walk them in
	// a stable order (positions make the output order anyway).
	names := make([]string, 0, len(trees))
	for name := range trees {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if t := trees[name]; t.Root != nil {
			s.node(t.Root)
		}
	}
	return s.out, nil
}

type templateScan struct {
	file  string
	lines *lines
	out   []Usage
}

func (s *templateScan) node(n parse.Node) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			s.node(c)
		}
	case *parse.ActionNode:
		s.pipe(n.Pipe)
	case *parse.IfNode:
		s.branch(&n.BranchNode)
	case *parse.RangeNode:
		s.branch(&n.BranchNode)
	case *parse.WithNode:
		s.branch(&n.BranchNode)
	case *parse.TemplateNode:
		s.pipe(n.Pipe)
	case *parse.PipeNode:
		s.pipe(n)
	case *parse.ChainNode:
		s.node(n.Node)
	}
}

func (s *templateScan) branch(b *parse.BranchNode) {
	s.pipe(b.Pipe)
	s.node(b.List)
	if b.ElseList != nil {
		s.node(b.ElseList)
	}
}

// pipe reports the message calls in a pipeline and the pipelines nested
// in its arguments. A string literal piped into a bare t or th
// ({{"…" | t}}) is its first argument.
func (s *templateScan) pipe(p *parse.PipeNode) {
	if p == nil {
		return
	}
	for i, cmd := range p.Cmds {
		if len(cmd.Args) > 0 {
			if fn, ok := cmd.Args[0].(*parse.IdentifierNode); ok && templateFuncs[fn.Ident] {
				switch {
				case len(cmd.Args) > 1:
					s.literal(cmd.Args[1])
				case i > 0 && fn.Ident != "td" && len(p.Cmds[i-1].Args) == 1:
					s.literal(p.Cmds[i-1].Args[0])
				}
			}
		}
		for _, arg := range cmd.Args {
			s.node(arg)
		}
	}
}

func (s *templateScan) literal(n parse.Node) {
	str, ok := n.(*parse.StringNode)
	if !ok || !validKey(str.Text) {
		return
	}
	// Pos is the opening quote's offset; the key starts right after it.
	s.out = append(s.out, s.lines.usage(str.Text, s.file, int(str.Pos)+1, KindTemplate))
}
