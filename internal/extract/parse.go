package extract

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Product is what the parser found on a page. Any field can be empty.
type Product struct {
	Title    string
	Images   []string // absolute URLs, best candidate first
	Price    string   // decimal with "." as separator, for example "89.50"
	Currency string   // upper-case ISO 4217 code
	Options  []Option // sizes, colors and the like the page offers, at most [MaxOptions]
}

var (
	metaTagRe   = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	linkTagRe   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	attrRe      = regexp.MustCompile(`(?s)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	titleRe     = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	jsonLDRe    = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	spaceRe     = regexp.MustCompile(`\s+`)
	priceCharRe = regexp.MustCompile(`[^0-9.,]`)
)

// Parse extracts product details from an HTML page. It prefers JSON-LD
// Product data, then OpenGraph, then plain meta tags and the <title>. Each
// field falls back separately. base is the page URL, used to resolve
// relative image URLs.
func Parse(body []byte, base string) Product {
	p, _ := parse(body, base)
	return p
}

// parse also reports whether the page has a product signal: a JSON-LD
// Product, an og:title or og:image, or a price. A bare <title> is not one,
// because challenge pages that return 200 often have only a <title>.
func parse(body []byte, base string) (p Product, signal bool) {
	src := string(body)
	metas := collectMeta(src)
	ld := parseJSONLD(src)

	p.Title = firstNonEmpty(ld.Title, metas.get("og:title"), metas.get("twitter:title"), cleanText(firstMatch(titleRe, src)))
	p.Price = firstNonEmpty(ld.Price, normalizePrice(metas.get("product:price:amount")),
		normalizePrice(metas.get("og:price:amount")), normalizePrice(metas.get("price")))
	if p.Price != "" {
		p.Currency = strings.ToUpper(strings.TrimSpace(firstNonEmpty(ld.Currency, metas.get("product:price:currency"),
			metas.get("og:price:currency"), metas.get("pricecurrency"))))
	}

	var imgs []string
	imgs = append(imgs, ld.Images...)
	imgs = append(imgs, metas.all("og:image")...)
	imgs = append(imgs, metas.all("og:image:secure_url")...)
	imgs = append(imgs, metas.all("twitter:image")...)
	imgs = append(imgs, metas.all("twitter:image:src")...)
	imgs = append(imgs, imageSrcLinks(src)...)
	p.Images = resolveAll(imgs, base)
	p.Options = parseOptions(src, ld.node, base)
	signal = ld.found || p.Price != "" || metas.get("og:title") != "" || metas.get("og:image") != ""
	return p, signal
}

type metaSet struct{ m map[string][]string }

func (s metaSet) get(k string) string {
	if v := s.m[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}
func (s metaSet) all(k string) []string { return s.m[k] }

func parseAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		name := strings.ToLower(m[1])
		if _, dup := attrs[name]; dup {
			continue
		}
		attrs[name] = html.UnescapeString(firstNonEmpty(m[2], m[3], m[4]))
	}
	return attrs
}

func collectMeta(src string) metaSet {
	s := metaSet{m: map[string][]string{}}
	for _, tag := range metaTagRe.FindAllString(src, -1) {
		a := parseAttrs(tag)
		key := strings.ToLower(firstNonEmpty(a["property"], a["name"], a["itemprop"]))
		val := strings.TrimSpace(a["content"])
		if key != "" && val != "" {
			s.m[key] = append(s.m[key], val)
		}
	}
	return s
}

func imageSrcLinks(src string) []string {
	var out []string
	for _, tag := range linkTagRe.FindAllString(src, -1) {
		a := parseAttrs(tag)
		if strings.EqualFold(strings.TrimSpace(a["rel"]), "image_src") && a["href"] != "" {
			out = append(out, a["href"])
		}
	}
	return out
}

type ldResult struct {
	Product
	found bool
	node  map[string]any // the product's JSON-LD object
}

func parseJSONLD(src string) ldResult {
	for _, m := range jsonLDRe.FindAllStringSubmatch(src, -1) {
		if !strings.Contains(strings.ToLower(parseAttrs(m[1])["type"]), "ld+json") {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(m[2])))
		dec.UseNumber()
		var doc any
		if err := dec.Decode(&doc); err != nil {
			continue
		}
		for _, node := range flattenNodes(doc) {
			if isProduct(node) {
				return ldResult{Product: productFromNode(node), found: true, node: node}
			}
		}
	}
	return ldResult{}
}

// flattenNodes lists the objects in a JSON-LD document, including the
// contents of arrays and @graph.
func flattenNodes(v any) []map[string]any {
	switch t := v.(type) {
	case []any:
		var out []map[string]any
		for _, e := range t {
			out = append(out, flattenNodes(e)...)
		}
		return out
	case map[string]any:
		out := []map[string]any{t}
		if g, ok := t["@graph"]; ok {
			out = append(out, flattenNodes(g)...)
		}
		return out
	}
	return nil
}

func isProduct(n map[string]any) bool {
	for _, t := range toSlice(n["@type"]) {
		if s, ok := t.(string); ok && (strings.HasSuffix(s, "Product") || strings.HasSuffix(s, "ProductGroup")) {
			return true
		}
	}
	return false
}

func toSlice(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	default:
		return []any{t}
	}
}

func productFromNode(n map[string]any) Product {
	p := Product{Title: cleanText(str(n["name"]))}
	for _, img := range toSlice(n["image"]) {
		switch t := img.(type) {
		case string:
			p.Images = append(p.Images, t)
		case map[string]any:
			if u := str(t["url"]); u != "" {
				p.Images = append(p.Images, u)
			} else if u := str(t["contentUrl"]); u != "" {
				p.Images = append(p.Images, u)
			}
		}
	}
	p.Price, p.Currency = offerPrice(n["offers"])
	return p
}

// offerPrice returns the first priced offer. AggregateOffer uses lowPrice.
func offerPrice(v any) (price, currency string) {
	for _, o := range toSlice(v) {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		price = normalizePrice(str(m["price"]))
		if price == "" {
			price = normalizePrice(str(m["lowPrice"]))
		}
		currency = str(m["priceCurrency"])
		if price == "" {
			for _, spec := range toSlice(m["priceSpecification"]) {
				if sm, ok := spec.(map[string]any); ok {
					price = normalizePrice(str(sm["price"]))
					currency = firstNonEmpty(str(sm["priceCurrency"]), currency)
					if price != "" {
						break
					}
				}
			}
		}
		if price == "" {
			price, currency = offerPrice(m["offers"])
		}
		if price != "" {
			return price, strings.ToUpper(strings.TrimSpace(currency))
		}
	}
	return "", ""
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// normalizePrice turns "1,299.00", "45,99", or "$19.99" into a plain decimal
// string with "." as the separator. It returns "" if there is no number.
func normalizePrice(s string) string {
	s = priceCharRe.ReplaceAllString(s, "")
	s = strings.Trim(s, ".,")
	if s == "" {
		return ""
	}
	lastDot, lastComma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case lastDot >= 0 && lastComma >= 0:
		if lastComma > lastDot { // 1.299,00
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else { // 1,299.00
			s = strings.ReplaceAll(s, ",", "")
		}
	case lastComma >= 0:
		if strings.Count(s, ",") == 1 && len(s)-lastComma-1 != 3 { // 45,99
			s = strings.Replace(s, ",", ".", 1)
		} else { // 1,299
			s = strings.ReplaceAll(s, ",", "")
		}
	}
	if strings.Count(s, ".") > 1 {
		return ""
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return ""
	}
	return s
}

func resolveAll(refs []string, base string) []string {
	b, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range refs {
		r = strings.TrimSpace(html.UnescapeString(r))
		if r == "" || strings.HasPrefix(r, "data:") {
			continue
		}
		u, err := b.Parse(r)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			continue
		}
		if s := u.String(); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstMatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func cleanText(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(s), " "))
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
