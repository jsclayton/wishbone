package loginlink

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/session"
)

// The alphabet has 32 characters, so each is 5 bits, and none of the look-alikes.
func TestTheLastingAlphabetHas32CharactersAndNoLookAlikes(t *testing.T) {
	require.Len(t, LastingAlphabet, 32)
	seen := map[rune]bool{}
	for _, c := range LastingAlphabet {
		require.False(t, seen[c], "%c is in the alphabet twice", c)
		seen[c] = true
		require.True(t, (c >= '2' && c <= '9') || (c >= 'a' && c <= 'z'), "%c: digits and lower-case letters only", c)
	}
	for _, c := range "0O1lIo" {
		require.NotContains(t, LastingAlphabet, string(c))
	}
}

// A token is 16 characters of the alphabet, shown as four groups of four, and
// the hash is of the 16 characters with no hyphens: 80 bits.
func TestALastingTokenIsShownAsFourGroupsOfFour(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		shown, hash, err := newLastingToken()
		require.NoError(t, err)
		groups := strings.Split(shown, "-")
		require.Len(t, groups, 4, shown)
		for _, g := range groups {
			require.Len(t, g, 4, shown)
			for _, c := range g {
				require.Contains(t, LastingAlphabet, string(c), shown)
			}
		}
		canonical := strings.Join(groups, "")
		require.Equal(t, session.Hash(canonical), hash)
		require.False(t, seen[canonical], "a token comes once")
		seen[canonical] = true
	}
}

func TestALastingTokenIsTakenInAnyCaseWithOrWithoutHyphens(t *testing.T) {
	const want = "k7mq3xtdp9whr2fn"
	for _, form := range []string{"k7mq-3xtd-p9wh-r2fn", want, "K7MQ-3XTD-P9WH-R2FN", "K7mq3xtdP9whR2fn", "k7mq3xtd-p9whr2fn", "-k7mq-3xtd-p9wh-r2fn-"} {
		got, ok := lastingCanonical(form)
		require.True(t, ok, form)
		require.Equal(t, want, got, form)
	}
	for _, form := range []string{
		"", "k7mq-3xtd-p9wh-r2f", "k7mq-3xtd-p9wh-r2fnn", "k7mq 3xtd p9wh r2fn", "k7mq_3xtd_p9wh_r2fn",
		"o7mq-3xtd-p9wh-r2fn", "07mq-3xtd-p9wh-r2fn", "17mq-3xtd-p9wh-r2fn", "l7mq-3xtd-p9wh-r2fn",
		// Unicode case folding takes the Kelvin sign (U+212A) as k, so these
		// would be the first form. Only A to Z are upper case.
		"\u212a7mq-3xtd-p9wh-r2fn", "\u212a7mq3xtdp9whr2fn",
		// 16 bytes, but not 16 characters of the alphabet.
		"\u212a7mq3xtdp9whr2",
		// An ordinary login link's token: 43 characters of base64url.
		"Zk3pQ9x-ab_cdEfGhIjKlMnOpQrStUvWxYz0123456",
	} {
		_, ok := lastingCanonical(form)
		require.False(t, ok, form)
	}
}

func TestAShownTokenHasItsHyphensAfterEachFourCharacters(t *testing.T) {
	require.Equal(t, "k7mq-3xtd-p9wh-r2fn", showLasting("k7mq3xtdp9whr2fn"))
}
