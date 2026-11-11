package occasion

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func d(y int, m time.Month, day int) Date { return Date{y, m, day} }

func TestNext(t *testing.T) {
	tests := []struct {
		name   string
		rule   Rule
		from   Date
		want   Date
		wantOK bool
	}{
		{"fixed later this year", Annual(time.December, 25), d(2026, 3, 1), d(2026, 12, 25), true},
		{"fixed on the day", Annual(time.December, 25), d(2026, 12, 25), d(2026, 12, 25), true},
		{"fixed day after rolls over", Annual(time.December, 25), d(2026, 12, 26), d(2027, 12, 25), true},
		{"feb 29 in leap year", Annual(time.February, 29), d(2028, 1, 1), d(2028, 2, 29), true},
		{"feb 29 in non-leap year is feb 28", Annual(time.February, 29), d(2027, 1, 1), d(2027, 2, 28), true},
		{"feb 29 after feb 28 non-leap goes to next year", Annual(time.February, 29), d(2027, 3, 1), d(2028, 2, 29), true},
		{"feb 29 century non-leap", Annual(time.February, 29), d(2100, 1, 1), d(2100, 2, 28), true},
		{"feb 29 400-year leap", Annual(time.February, 29), d(2000, 1, 1), d(2000, 2, 29), true},
		{"one-time future", Once(2027, time.May, 14), d(2026, 1, 1), d(2027, 5, 14), true},
		{"one-time on the day", Once(2027, time.May, 14), d(2027, 5, 14), d(2027, 5, 14), true},
		{"one-time past", Once(2027, time.May, 14), d(2027, 5, 15), Date{}, false},
		{"4th thursday of november 2026", NthWeekday(4, time.Thursday, time.November), d(2026, 1, 1), d(2026, 11, 26), true},
		{"4th thursday after it passed", NthWeekday(4, time.Thursday, time.November), d(2026, 11, 27), d(2027, 11, 25), true},
		{"1st weekday when month starts on it", NthWeekday(1, time.Sunday, time.February), d(2026, 1, 1), d(2026, 2, 1), true},
		{"4th weekday when month starts on it", NthWeekday(4, time.Sunday, time.February), d(2026, 1, 1), d(2026, 2, 22), true},
		{"4th friday latest possible (day 28)", NthWeekday(4, time.Friday, time.November), d(2025, 1, 1), d(2025, 11, 28), true},
		{"2nd sunday of may 2026", NthWeekday(2, time.Sunday, time.May), d(2026, 1, 1), d(2026, 5, 10), true},
		{"last monday of may 2026 (4 mondays)", LastWeekday(time.Monday, time.May), d(2026, 1, 1), d(2026, 5, 25), true},
		{"last monday of may 2027 (5 mondays)", LastWeekday(time.Monday, time.May), d(2027, 1, 1), d(2027, 5, 31), true},
		{"last tuesday of feb leap year", LastWeekday(time.Tuesday, time.February), d(2028, 1, 1), d(2028, 2, 29), true},
		{"last weekday after passed", LastWeekday(time.Monday, time.May), d(2026, 5, 26), d(2027, 5, 31), true},
		{"year boundary", Annual(time.January, 1), d(2026, 12, 31), d(2027, 1, 1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.rule.Next(tt.from)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("Next(%v) = %v, %v; want %v, %v", tt.from, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestBetween(t *testing.T) {
	tests := []struct {
		name     string
		rule     Rule
		from, to Date
		want     []Date
	}{
		{"start inclusive", Annual(time.March, 1), d(2026, 3, 1), d(2026, 3, 31), []Date{d(2026, 3, 1)}},
		{"end inclusive", Annual(time.March, 31), d(2026, 3, 1), d(2026, 3, 31), []Date{d(2026, 3, 31)}},
		{"before start excluded", Annual(time.March, 1), d(2026, 3, 2), d(2026, 3, 31), nil},
		{"after end excluded", Annual(time.April, 1), d(2026, 3, 2), d(2026, 3, 31), nil},
		{"window spanning years", Annual(time.January, 1), d(2026, 12, 1), d(2028, 1, 1), []Date{d(2027, 1, 1), d(2028, 1, 1)}},
		{"window longer than a year has multiple", Annual(time.June, 1), d(2026, 1, 1), d(2028, 12, 31), []Date{d(2026, 6, 1), d(2027, 6, 1), d(2028, 6, 1)}},
		{"one-time inside", Once(2026, time.May, 14), d(2026, 5, 1), d(2026, 6, 1), []Date{d(2026, 5, 14)}},
		{"one-time past", Once(2025, time.May, 14), d(2026, 5, 1), d(2026, 6, 1), nil},
		{"empty when to before from", Annual(time.June, 1), d(2026, 6, 2), d(2026, 6, 1), nil},
		{"feb 29 window", Annual(time.February, 29), d(2027, 1, 1), d(2028, 12, 31), []Date{d(2027, 2, 28), d(2028, 2, 29)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.Between(tt.from, tt.to)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestAgeTurning(t *testing.T) {
	if got := AgeTurning(1990, d(2026, 7, 4)); got != 36 {
		t.Fatalf("got %d, want 36", got)
	}
	// Feb 29 2000 birthday, occurrence on Feb 28 2027 -> turning 27.
	if got := AgeTurning(2000, d(2027, 2, 28)); got != 27 {
		t.Fatalf("got %d, want 27", got)
	}
}

func TestRevealAt(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	sp := mustLoad(t, "America/Sao_Paulo")
	apia := mustLoad(t, "Pacific/Apia")
	tests := []struct {
		name string
		date Date
		loc  *time.Location
		want time.Time
	}{
		{"plain", d(2026, 12, 25), time.UTC, time.Date(2026, 12, 26, 0, 0, 0, 0, time.UTC)},
		{"month end", d(2026, 12, 31), time.UTC, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"feb 28 leap year", d(2028, 2, 28), time.UTC, time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"in zone", d(2026, 12, 25), ny, time.Date(2026, 12, 26, 5, 0, 0, 0, time.UTC)},
		// US spring forward is Sun 2026-03-08; day after Sat is a 23-hour day.
		{"spring forward day after", d(2026, 3, 7), ny, time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)},
		{"day after spring forward", d(2026, 3, 8), ny, time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)},
		// US fall back is Sun 2026-11-01.
		{"fall back day after", d(2026, 10, 31), ny, time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)},
		{"day after fall back", d(2026, 11, 1), ny, time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)},
		// Sao Paulo 2018: midnight Nov 4 did not exist (clocks jumped to 01:00).
		{"midnight skipped by DST", d(2018, 11, 3), sp, time.Date(2018, 11, 4, 3, 0, 0, 0, time.UTC)},
		// Samoa skipped 2011-12-30 entirely; reveal is the first instant on or after it.
		{"whole day skipped", d(2011, 12, 29), apia, time.Date(2011, 12, 30, 10, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RevealAt(tt.date, tt.loc)
			if !got.Equal(tt.want) {
				t.Fatalf("got %v, want %v", got.UTC(), tt.want)
			}
		})
	}
}

func TestDateOf(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	now := time.Date(2026, 12, 26, 3, 0, 0, 0, time.UTC) // still Dec 25 in New York
	if got := DateOf(now, ny); got != d(2026, 12, 25) {
		t.Fatalf("got %v", got)
	}
}

func TestValidate(t *testing.T) {
	bad := []Rule{
		Annual(time.February, 30),
		Annual(time.April, 31),
		Annual(13, 1),
		Annual(time.May, 0),
		Once(2026, time.February, 29),
		NthWeekday(0, time.Monday, time.May),
		NthWeekday(5, time.Monday, time.May),
		NthWeekday(1, 7, time.May),
		LastWeekday(time.Monday, 0),
		{},
	}
	for _, r := range bad {
		if r.Validate() == nil {
			t.Errorf("expected %+v invalid", r)
		}
	}
	good := []Rule{
		Annual(time.February, 29), Once(2028, time.February, 29),
		NthWeekday(4, time.Thursday, time.November), LastWeekday(time.Monday, time.May),
	}
	for _, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}
