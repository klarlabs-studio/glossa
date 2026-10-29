package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	mf "go.klarlabs.de/glossa/messageformat"
)

// Normalized is a message in translation-memory form: its pattern text
// with every placeholder replaced by its position ({$amount} → {1}),
// markup kept as tags (<b>, </b>, <br/>), whitespace collapsed and NFC
// applied. Matches then ignore variable names but keep structure; the
// signature adds the placeholders' types, which the text leaves out.
type Normalized struct {
	// Text is what fuzzy matching compares. A select message becomes
	// ".match {1}" followed by one "keys {pattern}" line per variant.
	Text string
	// Hash is the hex SHA-256 of Text: the exact-match key.
	Hash string
	// Signature lists each position's type, and selector kind for
	// selectors: "1:number/plural,2:string". Empty without placeholders.
	Signature string
	// Vars are the variable names by position: Vars[0] is {1}.
	Vars []string
}

// Normalize computes m's translation-memory form.
func Normalize(m mf.Message) Normalized {
	args := mf.Arguments(m)
	w := normWriter{pos: map[string]int{}, locals: localRoots(m)}
	for i, a := range args {
		w.pos[a.Name] = i + 1
	}
	var b strings.Builder
	if m.IsSelect() {
		b.WriteString(".match")
		for _, s := range m.Selectors {
			b.WriteString(" ")
			b.WriteString(w.variable(s.Name))
		}
		for _, v := range m.Variants {
			b.WriteString("\n")
			for i, k := range v.Keys {
				if i > 0 {
					b.WriteString(" ")
				}
				if k.Catchall {
					b.WriteString("*")
				} else {
					b.WriteString(escapeText(k.Value))
				}
			}
			b.WriteString(" {")
			b.WriteString(w.pattern(v.Value))
			b.WriteString("}")
		}
	} else {
		b.WriteString(w.pattern(m.Pattern))
	}
	text := norm.NFC.String(b.String())
	sum := sha256.Sum256([]byte(text))
	sig := make([]string, len(args))
	vars := make([]string, len(args))
	for i, a := range args {
		vars[i] = a.Name
		sig[i] = strconv.Itoa(i+1) + ":" + string(a.Type)
		if a.Selector != nil {
			sig[i] += "/" + string(a.Selector.Kind)
		}
	}
	return Normalized{Text: text, Hash: hex.EncodeToString(sum[:]), Signature: strings.Join(sig, ","), Vars: vars}
}

type normWriter struct {
	pos    map[string]int
	locals map[string]string
}

// variable renders a variable reference by the position of the
// argument it resolves to.
func (w normWriter) variable(name string) string {
	root := name
	if r, ok := w.locals[name]; ok {
		root = r
	}
	if p, ok := w.pos[root]; ok {
		return "{" + strconv.Itoa(p) + "}"
	}
	return "{?}"
}

func (w normWriter) pattern(p mf.Pattern) string {
	var b strings.Builder
	for _, el := range p {
		switch el := el.(type) {
		case mf.Text:
			b.WriteString(escapeText(string(el)))
		case mf.Expression:
			switch arg := el.Arg.(type) {
			case mf.VariableRef:
				b.WriteString(w.variable(arg.Name))
			case mf.Literal:
				b.WriteString(escapeText(arg.Value))
			default:
				if el.Function != nil {
					b.WriteString("{:" + el.Function.Name + "}")
				}
			}
		case mf.Markup:
			switch el.Kind {
			case mf.MarkupOpen:
				b.WriteString("<" + el.Name + ">")
			case mf.MarkupClose:
				b.WriteString("</" + el.Name + ">")
			default:
				b.WriteString("<" + el.Name + "/>")
			}
		}
	}
	return collapseSpace(b.String())
}

// localRoots maps each .local variable to the argument it is bound to,
// following chains of locals.
func localRoots(m mf.Message) map[string]string {
	direct := map[string]string{}
	for _, d := range m.Declarations {
		if d.Type != mf.LocalDeclaration {
			continue
		}
		if ref, ok := d.Value.Arg.(mf.VariableRef); ok {
			direct[d.Name] = ref.Name
		}
	}
	roots := map[string]string{}
	for name := range direct {
		cur, seen := name, map[string]bool{}
		for {
			next, ok := direct[cur]
			if !ok || seen[next] {
				break
			}
			seen[cur] = true
			cur = next
		}
		roots[name] = cur
	}
	return roots
}

// escapeText keeps literal braces and angle brackets distinguishable
// from the placeholders and tags normalization writes.
func escapeText(s string) string {
	if !strings.ContainsAny(s, `\{}<>`) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\{}<>`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// collapseSpace trims s and collapses runs of white space to one space.
func collapseSpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// ObjectReplacement stands in for a placeholder in VisibleText, so the
// words around it stay apart and no variable name reads as a word.
const ObjectReplacement = "￼"

// VisibleText is the literal text of a message as a reader sees it:
// each pattern's text with placeholders as U+FFFC and markup dropped,
// variants on their own lines. Term recognition and terminology QA run
// over it, so a variable named {$workspace} is never taken for the word.
func VisibleText(m mf.Message) string {
	lines := make([]string, 0, len(m.Patterns()))
	for _, p := range m.Patterns() {
		var b strings.Builder
		for _, el := range p {
			switch el := el.(type) {
			case mf.Text:
				b.WriteString(string(el))
			case mf.Expression:
				if lit, ok := el.Arg.(mf.Literal); ok {
					b.WriteString(lit.Value)
				} else {
					b.WriteString(ObjectReplacement)
				}
			}
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}
