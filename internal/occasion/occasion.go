// Package occasion owns the occasions table: a user's birthday occasion and
// custom occasions, and who sees and changes them.
//
// This file is the date rule: it turns a rule into dates. It is pure: it never
// reads the clock, and it has no database or HTTP dependencies. The rest of the
// package is a domain package (see the layout notes in package user). Who can
// see an occasion and who can change one is in rules.go, and nowhere else.
package occasion

import (
	"errors"
	"fmt"
	"time"
)

// Date is a calendar date with no time of day or zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the calendar date of t in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return Date{y, m, d}
}

// ParseDate reads a date written as YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, errors.New("occasion: a date is written YYYY-MM-DD")
	}
	return DateOf(t, time.UTC), nil
}

func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }

// Before reports whether d is an earlier date than o.
func (d Date) Before(o Date) bool {
	if d.Year != o.Year {
		return d.Year < o.Year
	}
	if d.Month != o.Month {
		return d.Month < o.Month
	}
	return d.Day < o.Day
}

// Kind is the kind of date rule.
type Kind int

const (
	kindInvalid Kind = iota
	KindAnnual
	KindOnce
	KindNthWeekday
	KindLastWeekday
)

// Rule is an occasion's date rule. Build one with Annual, Once, NthWeekday,
// or LastWeekday.
type Rule struct {
	Kind    Kind
	Year    int // KindOnce only
	Month   time.Month
	Day     int // KindAnnual and KindOnce
	Weekday time.Weekday
	N       int // KindNthWeekday only, 1-4
}

// Annual is a fixed date every year. Feb 29 falls on Feb 28 in non-leap years.
func Annual(m time.Month, day int) Rule { return Rule{Kind: KindAnnual, Month: m, Day: day} }

// Once is a single date.
func Once(year int, m time.Month, day int) Rule {
	return Rule{Kind: KindOnce, Year: year, Month: m, Day: day}
}

// NthWeekday is the nth (1-4) weekday of a month, every year.
func NthWeekday(n int, wd time.Weekday, m time.Month) Rule {
	return Rule{Kind: KindNthWeekday, N: n, Weekday: wd, Month: m}
}

// LastWeekday is the last weekday of a month, every year.
func LastWeekday(wd time.Weekday, m time.Month) Rule {
	return Rule{Kind: KindLastWeekday, Weekday: wd, Month: m}
}

// Validate reports whether the rule is well formed.
func (r Rule) Validate() error {
	if r.Month < time.January || r.Month > time.December {
		return errors.New("occasion: month out of range")
	}
	if r.Kind != KindAnnual && r.Kind != KindOnce && r.Kind != KindNthWeekday && r.Kind != KindLastWeekday {
		return errors.New("occasion: unknown rule kind")
	}
	if r.Kind == KindNthWeekday || r.Kind == KindLastWeekday {
		if r.Weekday < time.Sunday || r.Weekday > time.Saturday {
			return errors.New("occasion: weekday out of range")
		}
	}
	switch r.Kind {
	case KindAnnual:
		// Validate against a leap year so Feb 29 is allowed.
		if r.Day < 1 || r.Day > daysIn(2000, r.Month) {
			return errors.New("occasion: day out of range")
		}
	case KindOnce:
		if r.Day < 1 || r.Day > daysIn(r.Year, r.Month) {
			return errors.New("occasion: day out of range")
		}
	case KindNthWeekday:
		if r.N < 1 || r.N > 4 {
			return errors.New("occasion: n must be 1 to 4")
		}
	}
	return nil
}

func daysIn(year int, m time.Month) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// inYear returns the rule's date in the given year.
func (r Rule) inYear(year int) Date {
	switch r.Kind {
	case KindAnnual:
		day := r.Day
		if n := daysIn(year, r.Month); day > n {
			day = n // Feb 29 in a non-leap year
		}
		return Date{year, r.Month, day}
	case KindNthWeekday:
		first := time.Date(year, r.Month, 1, 0, 0, 0, 0, time.UTC).Weekday()
		offset := (int(r.Weekday) - int(first) + 7) % 7
		return Date{year, r.Month, 1 + offset + 7*(r.N-1)}
	case KindLastWeekday:
		last := daysIn(year, r.Month)
		wd := time.Date(year, r.Month, last, 0, 0, 0, 0, time.UTC).Weekday()
		back := (int(wd) - int(r.Weekday) + 7) % 7
		return Date{year, r.Month, last - back}
	default:
		panic("occasion: inYear called on a rule that has no yearly date")
	}
}

// Next returns the first occurrence on or after from. It reports false when
// there is none (a one-time date in the past). The rule must be valid.
func (r Rule) Next(from Date) (Date, bool) {
	if r.Kind == KindOnce {
		d := Date{r.Year, r.Month, r.Day}
		if d.Before(from) {
			return Date{}, false
		}
		return d, true
	}
	d := r.inYear(from.Year)
	if d.Before(from) {
		d = r.inYear(from.Year + 1)
	}
	return d, true
}

// Between returns every occurrence from from to to, both inclusive, in order.
func (r Rule) Between(from, to Date) []Date {
	var out []Date
	for d, ok := r.Next(from); ok && !to.Before(d); d, ok = r.Next(Date{d.Year, d.Month, d.Day + 1}.norm()) {
		out = append(out, d)
		if r.Kind == KindOnce {
			break
		}
	}
	return out
}

// norm normalizes an overflowed day (for example day 32) into a real date.
func (d Date) norm() Date {
	return DateOf(time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC), time.UTC)
}

// AgeTurning is the age a member turns on an occurrence of their birthday,
// given the birth year. For a Feb 29 birthday the occurrence may be Feb 28.
func AgeTurning(birthYear int, occurrence Date) int { return occurrence.Year - birthYear }

// RevealAt is 00:00 on the day after the occurrence, in loc. If a DST change
// skips that midnight, or the whole day, it is the first instant on or after
// that day.
func RevealAt(occurrence Date, loc *time.Location) time.Time {
	next := occurrence.norm()
	next.Day++
	next = next.norm()
	t := time.Date(next.Year, next.Month, next.Day, 0, 0, 0, 0, loc)
	// time.Date may land before the skipped midnight; step forward to the day.
	for DateOf(t, loc).Before(next) {
		t = t.Add(time.Minute)
	}
	return t
}
