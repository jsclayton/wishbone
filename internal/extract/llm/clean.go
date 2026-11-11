package llm

import (
	"html"
	"regexp"
	"strings"

	"jsclayton.dev/wishbone/internal/extract"
)

// DefaultMaxText is the default cap, in characters, on cleaned page text.
const DefaultMaxText = 6000

const maxCandidateImages = 20

var (
	dropBlockRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template|iframe|nav|footer|select)\b[^>]*>.*?</(?:script|style|noscript|svg|template|iframe|nav|footer|select)\s*>`)
	commentRe   = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagRe       = regexp.MustCompile(`(?s)<[^>]*>`)
	blockEndRe  = regexp.MustCompile(`(?i)</?(p|div|li|ul|ol|br|tr|h[1-6]|section|article|table)\b[^>]*>`)
	spaceRe     = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankRe     = regexp.MustCompile(`\n\s*\n+`)
	titleTagRe  = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title>`)
	metaDescRe  = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	imgTagRe    = regexp.MustCompile(`(?is)<img\b[^>]*>`)
)

// CleanText turns an HTML page into plain text for the model. It keeps the
// <title> and meta description, drops scripts, styles, and boilerplate such as
// navigation and footers, collapses whitespace, and caps the result at max
// characters (DefaultMaxText if max <= 0).
func CleanText(page []byte, max int) string {
	if max <= 0 {
		max = DefaultMaxText
	}
	src := string(page)
	var head []string
	if t := extract.PageTitle(src); t != "" {
		head = append(head, "Title: "+t)
	}
	for _, tag := range metaDescRe.FindAllString(src, -1) {
		a := extract.ParseAttrs(tag)
		k := strings.ToLower(a["name"] + a["property"])
		if (k == "description" || k == "og:description") && a["content"] != "" {
			head = append(head, "Description: "+strings.TrimSpace(spaceRe.ReplaceAllString(a["content"], " ")))
			break
		}
	}

	body := commentRe.ReplaceAllString(src, " ")
	body = dropBlockRe.ReplaceAllString(body, " ")
	body = titleTagRe.ReplaceAllString(body, " ")
	body = blockEndRe.ReplaceAllString(body, "\n")
	body = tagRe.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)
	body = spaceRe.ReplaceAllString(body, " ")
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	body = strings.TrimSpace(blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n"))

	out := strings.TrimSpace(strings.Join(append(head, body), "\n"))
	if r := []rune(out); len(r) > max {
		out = strings.TrimSpace(string(r[:max]))
	}
	return out
}

// CandidateImages lists absolute http(s) image URLs from <img> tags, resolved
// against base. known (for example the parser's og:image list) comes first.
// The result has no duplicates and at most 20 entries.
func CandidateImages(page []byte, base string, known []string) []string {
	refs := append([]string(nil), known...)
	for _, tag := range imgTagRe.FindAllString(string(page), -1) {
		refs = append(refs, extract.ParseAttrs(tag)["src"])
	}
	out := extract.ResolveAll(refs, base)
	if len(out) > maxCandidateImages {
		out = out[:maxCandidateImages]
	}
	return out
}
