// Package textsafe makes untrusted strings (file names, paths, process
// names) safe to write to a terminal.
//
// A file name is attacker-controlled: any local user can create one in a
// shared directory such as /tmp. Written raw, control characters let it set
// the terminal title, write the clipboard (OSC 52), move the cursor or
// break the layout with a newline. Bidirectional overrides make a name
// display differently from its real bytes.
package textsafe

import (
	"strings"
	"unicode/utf8"
)

// Clean returns s with every byte sequence that could alter the terminal
// replaced by a visible stand-in:
//
//   - \n, \r and \t become the two-character escapes "\n", "\r", "\t";
//   - other C0 controls, DEL and C1 controls (U+0080-U+009F) become '?';
//   - bidirectional overrides and isolates become '?';
//   - invalid UTF-8 bytes become U+FFFD.
//
// Printable ASCII (the common case) is returned unchanged without allocating.
func Clean(s string) string {
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x7f {
			break
		}
	}
	if i == len(s) {
		return s
	}
	if utf8.ValidString(s) && !needsCleaning(s[i:]) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	b.WriteString(s[:i])
	for j := i; j < len(s); {
		c := s[j]
		if c < utf8.RuneSelf {
			switch {
			case c == '\n':
				b.WriteString(`\n`)
			case c == '\r':
				b.WriteString(`\r`)
			case c == '\t':
				b.WriteString(`\t`)
			case c < 0x20 || c == 0x7f:
				b.WriteByte('?')
			default:
				b.WriteByte(c)
			}
			j++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[j:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case unsafeRune(r):
			b.WriteByte('?')
		default:
			b.WriteString(s[j : j+size])
		}
		j += size
	}
	return b.String()
}

// needsCleaning reports whether a valid UTF-8 string contains a rune Clean
// would rewrite.
func needsCleaning(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unsafeRune(r) {
			return true
		}
	}
	return false
}

func unsafeRune(r rune) bool {
	switch {
	case r >= 0x80 && r <= 0x9f: // C1 controls (CSI, OSC, ... as single code points)
		return true
	case r >= 0x202a && r <= 0x202e: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	case r == 0x200e || r == 0x200f || r == 0x061c: // LRM, RLM, ALM
		return true
	}
	return false
}
