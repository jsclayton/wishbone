// Package initials works out the letters an avatar shows when a member has no
// photo. The server sends them so other members never need the last name.
package initials

import (
	"slices"
	"strings"
	"unicode"
)

// Of returns the first letter of the first name and of the last name, or only
// the first letter of the first name when there is no last name. It takes a
// whole letter, with any combining marks after it, not a byte. Names are
// trimmed first, and Latin and similar letters are upper-cased. Anything before
// the first letter, such as a quote, is skipped. A name with no letter keeps
// its first character.
func Of(first, last string) string {
	return letter(first) + letter(last)
}

func letter(name string) string {
	rs := []rune(strings.TrimSpace(name))
	if len(rs) == 0 {
		return ""
	}
	if i := slices.IndexFunc(rs, unicode.IsLetter); i > 0 {
		rs = rs[i:]
	}
	n := 1
	for n < len(rs) && unicode.In(rs[n], unicode.Mn, unicode.Me, unicode.Mc) {
		n++
	}
	return string(unicode.ToUpper(rs[0])) + string(rs[1:n])
}
