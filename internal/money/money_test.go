package money_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/money"
)

func TestMinorTurnsADecimalIntoTheCurrencysMinorUnit(t *testing.T) {
	for _, tc := range []struct {
		text, currency string
		want           int64
	}{
		{"89.50", "USD", 8950},
		{"89", "USD", 8900},
		{"1,299.99", "USD", 129999},
		{"0.5", "EUR", 50},
		{"1200", "JPY", 1200},
		{"3.250", "KWD", 3250},
		{" 12.00 ", "USD", 1200},
	} {
		got, ok := money.Minor(tc.text, tc.currency)
		require.True(t, ok, tc.text)
		require.Equal(t, tc.want, got, tc.text+" "+tc.currency)
	}
}

func TestMinorRefusesWhatIsNotAPrice(t *testing.T) {
	for _, tc := range []struct{ text, currency string }{
		{"", "USD"},
		{"free", "USD"},
		{"-5.00", "USD"},
		{"12.345", "USD"}, // more decimals than the currency has is never a rounding
		{"1200.5", "JPY"},
		{"1,29.99", "USD"},
		{"1.", "USD"},
		{".5", "USD"},
		{"99999999999999999999", "USD"},
	} {
		_, ok := money.Minor(tc.text, tc.currency)
		require.False(t, ok, tc.text+" "+tc.currency)
	}
}
