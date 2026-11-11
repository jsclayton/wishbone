package initials

import "testing"

func TestOf(t *testing.T) {
	for _, c := range []struct{ first, last, want string }{
		{"Nana Ruth", "Moore", "NM"},
		{"Uncle Dan", "", "U"},
		{"  meg ", " okafor ", "MO"},
		{"Émile", "Zola", "ÉZ"},
		{"Ørjan", "", "Ø"},
		{"李", "雷", "李雷"},
		{"élan", "", "É"}, // e + combining acute stays one letter
		{"", "", ""},
		{"", "Moore", "M"},
		{`"Bud"`, "Smith", "BS"},
		{"(Nana)", "", "N"},
		{"\"e\u0301lan\"", "", "E\u0301"}, // the mark stays with the letter after the skip
		{"🎉", "", "🎉"},
		{"42", "Moore", "4M"},
	} {
		if got := Of(c.first, c.last); got != c.want {
			t.Errorf("Of(%q, %q) = %q, want %q", c.first, c.last, got, c.want)
		}
	}
}
