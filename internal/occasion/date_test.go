package occasion

import (
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	d, err := ParseDate("2026-11-08")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Date{2026, time.November, 8}); d != want {
		t.Errorf("got %v, want %v", d, want)
	}
	for _, bad := range []string{"", "tomorrow", "2026-02-30", "2026-13-01", "2026-1-1", "08/11/2026", "2026-11-08T12:00:00Z"} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestDateBefore(t *testing.T) {
	a := Date{2026, time.November, 8}
	for _, tc := range []struct {
		b    Date
		want bool
	}{
		{Date{2026, time.November, 9}, true},
		{Date{2026, time.December, 1}, true},
		{Date{2027, time.January, 1}, true},
		{a, false},
		{Date{2026, time.November, 7}, false},
	} {
		if got := a.Before(tc.b); got != tc.want {
			t.Errorf("%v before %v: got %v", a, tc.b, got)
		}
	}
}
