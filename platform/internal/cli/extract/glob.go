package extract

import (
	"regexp"
	"strings"
)

// Glob is a compiled path pattern: `**` matches any number of path
// segments, `*` and `?` stay within one, `{a,b}` is alternation and
// `[...]` a character class. Paths use forward slashes.
type Glob struct {
	src string
	re  *regexp.Regexp
}

// CompileGlob compiles pattern.
func CompileGlob(pattern string) (*Glob, error) {
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth > 0 {
				depth--
				b.WriteString(")")
			} else {
				b.WriteString(`\}`)
			}
		case ',':
			if depth > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(",")
			}
		case '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := pattern[i+1 : i+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return &Glob{src: pattern, re: re}, nil
}

// Match reports whether the slash-separated path matches.
func (g *Glob) Match(path string) bool { return g.re.MatchString(path) }

func (g *Glob) String() string { return g.src }
