package extract

import (
	"path"
	"regexp"
	"strings"
)

// The lexical web scan: the fallback for HTML, Vue, Astro, TS, JS and JSX
// when @klarlabs-studio/glossa/unplugin doesn't run. It sees the call shapes, not the
// syntax around them, so unlike the plugin it doesn't skip comments.

var (
	// tCall is t("…") and $t("…"), bare or as a member call (.t("…")).
	tCall = regexp.MustCompile(`(?:^|[^\w$])\$?t\(\s*(["'` + "`" + `])([^"'` + "`" + `\n]*)(["'` + "`" + `])`)
	// openTag is the start of an element whose attributes may name a key.
	openTag = regexp.MustCompile(`<(glossa-(?:text|rich|plural|select)|GlossaText|T)(?:\s|/?>)`)
	// memberChain is a member chain (messages.checkout.pay); a call when
	// '(' follows it.
	memberChain = regexp.MustCompile(`[A-Za-z_$][\w$]*(?:\s*\.\s*[A-Za-z_$][\w$]*)+`)
	chainSep    = regexp.MustCompile(`\s*\.\s*`)
)

// scanWeb scans a web file lexically. tsNames maps accessor paths to keys.
func scanWeb(file string, src []byte, tsNames map[string]string) []Usage {
	s := &webScan{file: file, lines: newLines(src), src: string(src)}
	s.tCalls()
	s.tags()
	if path.Ext(file) != ".html" {
		s.accessors(tsNames)
	}
	component, route := webComponent(file)
	for i := range s.out {
		s.out[i].Component, s.out[i].Route = component, route
	}
	return s.out
}

// webComponent is the component (a Vue or Astro file's name) and route
// (an Astro page's pattern) of a file's usages.
func webComponent(file string) (component, route string) {
	ext := path.Ext(file)
	if ext != ".vue" && ext != ".astro" {
		return "", ""
	}
	component = strings.TrimSuffix(path.Base(file), ext)
	if ext == ".astro" && strings.HasPrefix(file, "src/pages/") {
		var segs []string
		for _, seg := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(file, "src/pages/"), ext), "/") {
			if seg != "index" {
				segs = append(segs, seg)
			}
		}
		route = "/" + strings.Join(segs, "/")
	}
	return component, route
}

type webScan struct {
	file  string
	lines *lines
	src   string
	out   []Usage
	seen  map[int]bool // offsets already reported
}

func (s *webScan) add(key string, offset int, kind string) {
	if !validKey(key) || s.seen[offset] {
		return
	}
	if s.seen == nil {
		s.seen = map[int]bool{}
	}
	s.seen[offset] = true
	s.out = append(s.out, s.lines.usage(key, s.file, offset, kind))
}

func (s *webScan) tCalls() {
	for _, m := range tCall.FindAllStringSubmatchIndex(s.src, -1) {
		if s.src[m[2]:m[3]] == s.src[m[6]:m[7]] {
			s.add(s.src[m[4]:m[5]], m[4], KindT)
		}
	}
}

// tags reports <glossa-text|rich|plural|select key|message> (key wins),
// <GlossaText id> and <T id>.
func (s *webScan) tags() {
	for _, m := range openTag.FindAllStringSubmatchIndex(s.src, -1) {
		name := s.src[m[2]:m[3]]
		attrs := parseAttrs(s.src, m[3])
		if strings.HasPrefix(name, "glossa-") {
			for _, want := range []string{"key", "message"} {
				if a, ok := attrs[want]; ok {
					s.add(a.value, a.offset, KindElement)
					break
				}
			}
			continue
		}
		for _, n := range []string{"id", ":id", "v-bind:id"} {
			a, ok := attrs[n]
			if !ok {
				continue
			}
			if n == "id" && !strings.HasPrefix(a.value, "{") {
				s.add(a.value, a.offset, KindComponent)
			} else if key, off, ok := quotedLiteral(a.value, a.offset); ok {
				s.add(key, off, KindComponent)
			}
			break
		}
	}
}

type attr struct {
	value  string
	offset int // of the value's first byte, inside its quotes
}

// parseAttrs reads the attributes of the tag whose name ends at i, up to
// its closing '>'. Values are quoted, braced (JSX) or bare.
func parseAttrs(src string, i int) map[string]attr {
	out := map[string]attr{}
	for i < len(src) {
		for i < len(src) && isSpace(src[i]) {
			i++
		}
		if i >= len(src) || src[i] == '>' || strings.HasPrefix(src[i:], "/>") {
			return out
		}
		start := i
		for i < len(src) && !isSpace(src[i]) && !strings.ContainsRune("=>/", rune(src[i])) {
			i++
		}
		name := src[start:i]
		if name == "" {
			i++ // a stray '/'
			continue
		}
		j := i
		for j < len(src) && isSpace(src[j]) {
			j++
		}
		if j >= len(src) || src[j] != '=' {
			continue // a boolean attribute
		}
		i = j + 1
		for i < len(src) && isSpace(src[i]) {
			i++
		}
		if i >= len(src) {
			return out
		}
		var a attr
		switch q := src[i]; q {
		case '"', '\'':
			end := strings.IndexByte(src[i+1:], q)
			if end < 0 {
				return out
			}
			a = attr{value: src[i+1 : i+1+end], offset: i + 1}
			i += end + 2
		case '{':
			end := matchingBrace(src, i)
			a = attr{value: src[i:end], offset: i}
			i = end
		default:
			start := i
			for i < len(src) && !isSpace(src[i]) && src[i] != '>' {
				i++
			}
			a = attr{value: src[start:i], offset: start}
		}
		if _, dup := out[name]; !dup {
			out[name] = a
		}
	}
	return out
}

// matchingBrace returns the offset after the '}' closing the '{' at i.
func matchingBrace(src string, i int) int {
	depth := 0
	for ; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(src)
}

// quotedLiteral reads a JS string literal that is a whole expression: a
// bound Vue value ('…') or a JSX container ({"…"}). It returns the
// literal's content and the offset of its first character.
func quotedLiteral(expr string, offset int) (string, int, bool) {
	inner := expr
	if strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}") {
		inner = inner[1 : len(inner)-1]
	}
	lead := len(inner) - len(strings.TrimLeft(inner, " \t\r\n"))
	start := offset + (len(expr)-len(inner))/2 + lead
	inner = strings.TrimSpace(inner)
	if len(inner) < 2 || !strings.ContainsRune(`"'`+"`", rune(inner[0])) || inner[len(inner)-1] != inner[0] {
		return "", 0, false
	}
	body := inner[1 : len(inner)-1]
	if strings.ContainsAny(body, `"'`+"`") || strings.Contains(body, "${") {
		return "", 0, false
	}
	return body, start + 1, true
}

// accessors reports typed accessor calls: <receiver>.<path>(…) where the
// path is a key's accessor path. The longest path wins; a one-segment
// path counts only on a receiver named messages.
func (s *webScan) accessors(tsNames map[string]string) {
	if len(tsNames) == 0 {
		return
	}
	for _, m := range memberChain.FindAllStringIndex(s.src, -1) {
		if !s.calledAt(m[0], m[1]) {
			continue
		}
		chain := s.src[m[0]:m[1]]
		segs := chainSep.Split(chain, -1)
		for k := 1; k < len(segs); k++ {
			key, ok := tsNames[strings.Join(segs[k:], ".")]
			if !ok || (k == len(segs)-1 && segs[k-1] != "messages") {
				continue
			}
			// The path's first segment: after the receiver and its dot.
			loc := chainSep.FindAllStringIndex(chain, -1)[k-1]
			s.add(key, m[0]+loc[1], KindAccessor)
			break
		}
	}
}

// calledAt reports whether the chain src[start:end] is a whole
// expression (not the tail of another chain or name) that is called.
func (s *webScan) calledAt(start, end int) bool {
	if start > 0 && (isIdent(s.src[start-1]) || s.src[start-1] == '.') {
		return false
	}
	rest := strings.TrimLeft(s.src[end:], " \t\r\n")
	return strings.HasPrefix(rest, "(")
}

func isIdent(b byte) bool {
	return b == '_' || b == '$' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }
