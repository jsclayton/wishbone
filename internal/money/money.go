// Package money turns the decimal prices that readers of a page or a photo give
// into the integers in a currency's minor unit that the API uses (docs/spec.md,
// API principles, Values).
package money

import (
	"strconv"
	"strings"
)

// minorDigits holds the ISO 4217 minor-unit exponent for currencies that don't
// use 2. Every other code uses 2. The web app shows prices with the browser's
// Intl digits, which agree for common currencies. If a code ever differs, the
// display is off by a power of 10, so keep this table in step with Intl.
var minorDigits = map[string]int{
	"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0, "KRW": 0,
	"PYG": 0, "RWF": 0, "UGX": 0, "UYI": 0, "VND": 0, "VUV": 0, "XAF": 0, "XOF": 0, "XPF": 0,
	"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
	"CLF": 4, "UYW": 4,
}

// FractionDigits is the number of decimal places in the currency's minor unit.
func FractionDigits(currency string) int {
	if d, ok := minorDigits[currency]; ok {
		return d
	}
	return 2
}

// Minor turns a decimal price such as "89.50" into minor units (8950 for USD).
// A comma groups thousands, a point is the decimal mark, and more decimals than
// the currency has is not a price, never a rounding. ok is false for anything
// that is not a non-negative price.
func Minor(text, currency string) (minor int64, ok bool) {
	t := strings.TrimSpace(text)
	whole, frac, hasPoint := strings.Cut(t, ".")
	if strings.Contains(whole, ",") {
		groups := strings.Split(whole, ",")
		if len(groups[0]) < 1 || len(groups[0]) > 3 {
			return 0, false
		}
		for _, g := range groups[1:] {
			if len(g) != 3 {
				return 0, false
			}
		}
		whole = strings.Join(groups, "")
	}
	if !digits(whole) || (hasPoint && !digits(frac)) {
		return 0, false
	}
	places := FractionDigits(currency)
	if len(frac) > places {
		return 0, false
	}
	n, err := strconv.ParseInt(whole+frac+strings.Repeat("0", places-len(frac)), 10, 64)
	return n, err == nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
