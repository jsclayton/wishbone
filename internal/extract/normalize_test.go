package extract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"already normal", "https://example.com/p/1?color=red", "https://example.com/p/1?color=red", false},
		{"host case", "https://WWW.Example.COM/Path/Case", "https://www.example.com/Path/Case", false},
		{"utm params", "https://example.com/p?utm_source=a&id=7&utm_medium=b", "https://example.com/p?id=7", false},
		{"only tracking", "https://example.com/p?gclid=1&fbclid=2", "https://example.com/p", false},
		{"other tracking", "https://example.com/p?msclkid=1&mc_eid=2&igshid=3&a=b", "https://example.com/p?a=b", false},
		{"keeps param order", "https://example.com/p?z=1&utm_x=2&a=2", "https://example.com/p?z=1&a=2", false},
		{"drops fragment", "https://example.com/p#reviews", "https://example.com/p", false},
		{"drops default https port", "https://example.com:443/p", "https://example.com/p", false},
		{"drops default http port", "http://example.com:80/p", "http://example.com/p", false},
		{"keeps other port", "https://example.com:8443/p", "https://example.com:8443/p", false},
		{"srsltid and hubspot", "https://example.com/p?srsltid=a&_hsenc=b&_hsmi=c&mkt_tok=d&id=1", "https://example.com/p?id=1", false},
		{"trims space", "  https://example.com/p  ", "https://example.com/p", false},
		{"missing scheme", "example.com/p?utm_source=x", "https://example.com/p", false},
		{"http kept", "http://example.com/p", "http://example.com/p", false},
		{"ftp rejected", "ftp://example.com/p", "", true},
		{"empty", "", "", true},
		{"no host", "https:///p", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeURL(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStoreFromURL(t *testing.T) {
	tests := map[string]string{
		"https://www.Example.com/p": "example.com",
		"https://shop.example.com/": "shop.example.com",
		"https://example.com:8080/": "example.com",
		"::bad":                     "",
		// A trailing dot names the same host.
		"https://temu.com./p/1":        "temu.com",
		"https://m.temu.com../p":       "m.temu.com",
		"https://www.temu.com./p":      "temu.com",
		"https://temu.com.:8080/p":     "temu.com",
		"https://ｔｅｍｕ.com/p":           "temu.com",
		"https://xn--bcher-kva.de/x":   "bücher.de",
		"https://BÜCHER.de/x":          "bücher.de",
		"https://203.0.113.7/x":        "203.0.113.7",
		"https://[2001:db8::1]:8443/x": "2001:db8::1",
		"https://a_b.temu.com/x":       "a_b.temu.com",
	}
	for in, want := range tests {
		require.Equal(t, want, StoreFromURL(in), in)
	}
}
