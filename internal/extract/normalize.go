// Package extract is stage 1 of the link extraction pipeline: it fetches a
// product page directly and pulls out the item details. It has no database,
// no HTTP routes, and no other stages. Later stages (Bright Data, LLM) plug in
// behind the [Fetcher] interface.
package extract

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// trackingParams are exact query parameter names that only track clicks.
var trackingParams = map[string]bool{
	"gclid": true, "gclsrc": true, "gbraid": true, "wbraid": true, "dclid": true,
	"fbclid": true, "msclkid": true, "yclid": true, "twclid": true, "ttclid": true,
	"igshid": true, "srsltid": true, "mkt_tok": true, "_hsenc": true, "_hsmi": true, "mc_cid": true, "mc_eid": true, "_ga": true, "_gl": true,
}

func isTrackingParam(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "utm_") || trackingParams[name]
}

// NormalizeURL returns the canonical form of a pasted URL: http or https only,
// lowercase host, no default port, no fragment, and no tracking parameters.
// The rest of the URL is kept, including query parameter order. A URL with no scheme gets https.
// It does not follow redirects; [Direct.Fetch] does that.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL scheme must be http or https")
	}
	if u.Hostname() == "" {
		return "", errors.New("URL has no host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	if u.RawQuery != "" {
		var kept []string
		for _, pair := range strings.Split(u.RawQuery, "&") {
			name, _, _ := strings.Cut(pair, "=")
			if unescaped, err := url.QueryUnescape(name); err == nil {
				name = unescaped
			}
			if pair != "" && !isTrackingParam(name) {
				kept = append(kept, pair)
			}
		}
		u.RawQuery = strings.Join(kept, "&")
	}
	return u.String(), nil
}

// StoreFromURL returns the store name for an offer: the lowercase host
// without a trailing dot and without a leading "www.". "temu.com." and
// "ｔｅｍｕ.com" name temu.com, and "xn--bcher-kva.de" names bücher.de (the
// Unicode form, as stored names already are). It returns "" if rawURL is not a
// valid URL.
func StoreFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	// ToUnicode returns its best result even with an error, so a host with an
	// underscore keeps its form. An error with no result keeps the host as is.
	if mapped, _ := idna.Lookup.ToUnicode(host); mapped != "" {
		host = mapped
	}
	host = strings.TrimRight(host, ".")
	return strings.TrimPrefix(host, "www.")
}
