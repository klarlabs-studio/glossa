package extract

import (
	"sort"
	"unicode/utf8"
)

// lines turns byte offsets in a file into the contract's positions:
// 1-based lines, and 1-based columns counted in Unicode code points (a
// tab is one column).
type lines struct {
	src    []byte
	starts []int // byte offset of each line's first byte
}

func newLines(src []byte) *lines {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lines{src: src, starts: starts}
}

// at returns the line and column of the byte at offset.
func (l *lines) at(offset int) (line, column int) {
	i := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
	return i + 1, utf8.RuneCount(l.src[l.starts[i]:offset]) + 1
}

// usage returns a usage of key whose first character is at offset.
func (l *lines) usage(key, file string, offset int, kind string) Usage {
	line, col := l.at(offset)
	return Usage{Key: key, File: file, Line: line, Column: col, Kind: kind}
}
