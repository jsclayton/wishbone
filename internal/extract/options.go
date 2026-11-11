package extract

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Option is one choice a product page offers, such as a size or a color.
type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values,omitempty"`
	// Selected is one of Values: the value the page or the link picks. It is
	// empty when nothing is picked or when the sources disagree.
	Selected string `json:"selected,omitempty"`
}

const (
	// MaxOptions is the most options a page gives.
	MaxOptions = 4
	// MaxOptionValues is the most values one option keeps.
	MaxOptionValues = 30
)

var (
	selectRe      = regexp.MustCompile(`(?is)<select\b([^>]*)>(.*?)</select>`)
	optionTagRe   = regexp.MustCompile(`(?is)<option\b([^>]*)>([^<]*)`)
	labelRe       = regexp.MustCompile(`(?is)<label\b([^>]*)>(.*?)</label>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	quotedRe      = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	selectedFlag  = regexp.MustCompile(`(?i)(^|\s)selected(\s|=|$)`)
	bracketName   = regexp.MustCompile(`(?i)^(?:options?|properties|attributes?)\[(.+)\]$`)
	placeholderRe = regexp.MustCompile(`(?i)^(?:choose|select|pick|please|--|—|-$)`)
)

// structuredPrefixes start the name of a form field or query parameter that
// carries a product option: WooCommerce, Shopify, Salesforce and the like.
var structuredPrefixes = []string{"attribute_pa_", "attribute_", "pa_", "option_"}

// optionWords are names a plain <select> may have, in its name or label. It
// has the British spellings that shops use.
var optionWords = words("size color colour style material width length finish flavor flavour scent pattern capacity storage fit edition format model shade variant option") //nolint:misspell // shops spell it both ways

// notOptionWords are names that rule a <select> out.
var notOptionWords = words("quantity qty country region state province currency language locale sort sorting filter shipping delivery month year day store zip postal page per")

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// optionBuilder gathers one option from every source.
type optionBuilder struct {
	name     string
	values   []optionValue
	selected []string // what each source picks, as value text
	fields   []string // normalized form field names of the <select>s that gave this option
	// conflicted is set when the link gave two different values for an option
	// the page lists no values for. The link's values are dropped then.
	conflicted bool
}

type optionValue struct {
	text string
	keys []string // normalized spellings that match this value
	link bool     // the link gave this value; the page doesn't list it
}

type optionSet struct{ list []*optionBuilder }

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func lastWord(s string) string {
	f := strings.Fields(strings.ToLower(s))
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

func oneWord(s string) bool { return norm(s) == norm(lastWord(s)) }

// pairs reports whether a one-word name, such as "Size", and a longer name
// with the same last word, such as "Shoe size", may be one option. Two longer
// names, such as "Band size" and "Cup size", are never one option.
func pairs(a, b string) bool {
	last := lastWord(a)
	return last != "" && last == lastWord(b) && oneWord(a) != oneWord(b)
}

// find returns the option that name refers to: the option with the same name,
// or else the only option that pairs with it.
func (s *optionSet) find(name string) *optionBuilder {
	var hit *optionBuilder
	count := 0
	for _, o := range s.list {
		if norm(o.name) == norm(name) {
			return o
		}
		if pairs(o.name, name) {
			hit = o
			count++
		}
	}
	if count != 1 {
		return nil
	}
	return hit
}

// byField returns the one option that a <select> with this field name gave.
func (s *optionSet) byField(field string) *optionBuilder {
	field = norm(field)
	if field == "" {
		return nil
	}
	var hit *optionBuilder
	for _, o := range s.list {
		if slices.Contains(o.fields, field) {
			if hit != nil {
				return nil
			}
			hit = o
		}
	}
	return hit
}

func (o *optionBuilder) addField(field string) {
	if f := norm(field); f != "" && !slices.Contains(o.fields, f) {
		o.fields = append(o.fields, f)
	}
}

// get returns the option with this name, and adds it if the set has none.
func (s *optionSet) get(name string) *optionBuilder {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	for _, o := range s.list {
		if norm(o.name) == norm(name) {
			return o
		}
	}
	o := &optionBuilder{name: name}
	s.list = append(s.list, o)
	return o
}

// joinPairs makes one option of a one-word option and the one longer name it
// pairs with, so JSON-LD "Size" and a <select> labeled "Shoe size" are one
// option. When "Size" pairs with two or more longer names, such as "Band size"
// and "Cup size", it can't be both, so all of them stay apart. The first of
// the two on the page keeps its name and place.
func (s *optionSet) joinPairs() {
	for i := 0; i < len(s.list); i++ {
		short := s.list[i]
		if !oneWord(short.name) {
			continue
		}
		var long []int
		for j, o := range s.list {
			if pairs(short.name, o.name) {
				long = append(long, j)
			}
		}
		if len(long) != 1 {
			continue
		}
		j := long[0]
		keep, drop := short, s.list[j]
		if j < i {
			keep, drop = drop, short
		}
		for _, v := range drop.values {
			keep.add(v.text, v.keys...)
		}
		keep.selected = append(keep.selected, drop.selected...)
		for _, f := range drop.fields {
			keep.addField(f)
		}
		at := i
		if j > i {
			at = j
		}
		s.list = append(s.list[:at], s.list[at+1:]...)
		i = -1 // start over: the list changed
	}
}

func (o *optionBuilder) add(text string, alt ...string) *optionValue {
	text = cleanText(text)
	if text == "" {
		return nil
	}
	for i := range o.values {
		if norm(o.values[i].text) == norm(text) {
			return &o.values[i]
		}
	}
	keys := []string{norm(text)}
	for _, a := range alt {
		if k := norm(a); k != "" {
			keys = append(keys, k)
		}
	}
	o.values = append(o.values, optionValue{text: text, keys: keys})
	return &o.values[len(o.values)-1]
}

// match returns the listed value that v names, by text or by its other spelling.
func (o *optionBuilder) match(v string) (string, bool) {
	k := norm(v)
	if k == "" {
		return "", false
	}
	for _, ov := range o.values {
		for _, vk := range ov.keys {
			if vk == k {
				return ov.text, true
			}
		}
	}
	return "", false
}

// pick records that a source selects v. A value the option doesn't list is
// added only when the option lists no values yet and addNew is true. Otherwise
// it is ignored: Wishbone doesn't guess.
func (o *optionBuilder) pick(v string, addNew bool) {
	if text, ok := o.match(v); ok {
		o.selected = append(o.selected, text)
		return
	}
	if !addNew || len(o.values) > 0 {
		return
	}
	if ov := o.add(v); ov != nil {
		o.selected = append(o.selected, ov.text)
	}
}

// pickLink is pick for a value from the link. A value it adds is marked, and
// a second, different one is a conflict: the link's values say nothing about
// which is right, so both are kept as answers and the values are dropped.
func (o *optionBuilder) pickLink(v string, addNew bool) {
	if o.conflicted {
		return
	}
	if _, ok := o.match(v); !ok && addNew && len(o.values) > 0 {
		for _, ov := range o.values {
			if !ov.link {
				return
			}
		}
		if t := cleanText(v); t != "" {
			o.selected = append(o.selected, t)
			o.values = nil
			o.conflicted = true
		}
		return
	}
	hadValues := len(o.values) > 0
	o.pick(v, addNew)
	if !hadValues && len(o.values) > 0 {
		o.values[0].link = true
	}
}

func (s *optionSet) result() []Option {
	var out []Option
	for _, o := range s.list {
		if len(out) == MaxOptions {
			break
		}
		opt := Option{Name: o.name}
		for i, v := range o.values {
			if i == MaxOptionValues {
				break
			}
			opt.Values = append(opt.Values, v.text)
		}
		// One answer is all the sources agree on. Two answers are a conflict.
		if len(o.selected) > 0 {
			agreed := true
			for _, s := range o.selected[1:] {
				agreed = agreed && norm(s) == norm(o.selected[0])
			}
			if agreed {
				for _, v := range opt.Values {
					if norm(v) == norm(o.selected[0]) {
						opt.Selected = v
					}
				}
			}
		}
		out = append(out, opt)
	}
	return out
}

// parseOptions reads the options a page offers from its JSON-LD product, its
// <select> elements, and the link's query. ld is the JSON-LD product node, if
// the page has one.
func parseOptions(src string, ld map[string]any, base string) []Option {
	set := &optionSet{}
	ldOptions(set, ld, base)
	selectOptions(set, src)
	set.joinPairs()
	queryOptions(set, base)
	return set.result()
}

// --- JSON-LD ---

// titleName turns a property or field name into words for display:
// "size" gives "Size", "suggestedGender" gives "Suggested gender".
func titleName(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(s))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, " ") {
		s = splitCamel(s)
	}
	if s == strings.ToLower(s) {
		return strings.ToUpper(s[:1]) + s[1:]
	}
	return s
}

// splitCamel puts a space before each upper-case letter that follows a
// lower-case one, and lowers it: "suggestedGender" gives "suggested gender".
// A name with no such letter, such as "Size" or "SKU", is not changed.
func splitCamel(s string) string {
	r := []rune(s)
	var b strings.Builder
	changed := false
	for i, c := range r {
		if i > 0 && unicode.IsUpper(c) && unicode.IsLower(r[i-1]) {
			b.WriteRune(' ')
			b.WriteRune(unicode.ToLower(c))
			changed = true
			continue
		}
		b.WriteRune(c)
	}
	if !changed {
		return s
	}
	return b.String()
}

// ldProp reads a variant's property with the spelling variesBy gives, such as
// suggestedGender, or else in lower case.
func ldProp(m map[string]any, p string) string {
	if v := ldValue(m[p]); v != "" {
		return v
	}
	return ldValue(m[strings.ToLower(p)])
}

// ldValue reads a JSON-LD property that is a string or a thing with a name.
func ldValue(v any) string {
	if s := str(v); s != "" {
		return cleanText(s)
	}
	if m, ok := v.(map[string]any); ok {
		return cleanText(firstNonEmpty(str(m["name"]), str(m["value"])))
	}
	return ""
}

func ldOptions(set *optionSet, n map[string]any, base string) {
	if n == nil {
		return
	}
	var names []string // property names, as the page spells them
	for _, v := range toSlice(n["variesBy"]) {
		s := str(v)
		if i := strings.LastIndexAny(s, "/#"); i >= 0 {
			s = s[i+1:]
		}
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	variants := toSlice(n["hasVariant"])
	if len(variants) == 0 {
		// A Product with a color or size of its own: the page has chosen it.
		if len(names) == 0 {
			for _, p := range []string{"size", "color"} {
				if v := ldValue(n[p]); v != "" && len(toSlice(n[p])) == 1 {
					o := set.get(titleName(p))
					o.pick(v, true)
				}
			}
			return
		}
		for _, p := range names {
			set.get(titleName(p))
		}
		return
	}
	if len(names) == 0 {
		for _, p := range []string{"size", "color"} {
			for _, v := range variants {
				if m, ok := v.(map[string]any); ok && ldProp(m, p) != "" {
					names = append(names, p)
					break
				}
			}
		}
	}
	chosen := matchVariant(variants, base)
	for _, p := range names {
		o := set.get(titleName(p))
		if o == nil {
			continue
		}
		for _, v := range variants {
			if m, ok := v.(map[string]any); ok {
				o.add(ldProp(m, p))
			}
		}
		if chosen != nil {
			if v := ldProp(chosen, p); v != "" {
				o.pick(v, false)
			}
		}
	}
}

// variantParams name the variant in a link. "id" is broad, but it is safe: a
// hit must also equal a variant's own sku, productID, mpn, or address query.
var variantParams = map[string]bool{"variant": true, "variant_id": true, "variantid": true, "sku": true, "pid": true, "skuid": true, "id": true}

// matchVariant returns the one variant the link points at, or nil. A variant
// matches when its own address has a query and the link has all of it, or when
// the link names its sku or id in a parameter such as variant=.
func matchVariant(variants []any, base string) map[string]any {
	b, err := url.Parse(base)
	if err != nil {
		return nil
	}
	q := b.Query()
	linkIDs := map[string]bool{}
	for k, vs := range q {
		if variantParams[strings.ToLower(k)] {
			for _, v := range vs {
				linkIDs[strings.ToLower(v)] = true
			}
		}
	}
	var found map[string]any
	count := 0
	for _, v := range variants {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		hit := false
		urls := []string{str(m["url"]), str(m["@id"])}
		for _, o := range toSlice(m["offers"]) {
			if om, ok := o.(map[string]any); ok {
				urls = append(urls, str(om["url"]))
			}
		}
		for _, raw := range urls {
			u, err := b.Parse(raw)
			if err != nil || raw == "" {
				continue
			}
			vq := u.Query()
			if len(vq) > 0 && u.Path == b.Path {
				all := true
				for k, vs := range vq {
					all = all && len(vs) > 0 && q.Get(k) == vs[0]
				}
				hit = hit || all
			}
			for k, vs := range vq {
				if variantParams[strings.ToLower(k)] && len(vs) > 0 && linkIDs[strings.ToLower(vs[0])] {
					hit = true
				}
			}
		}
		for _, p := range []string{"sku", "productID", "mpn"} {
			if id := strings.ToLower(str(m[p])); id != "" && linkIDs[id] {
				hit = true
			}
		}
		if hit {
			found = m
			count++
		}
	}
	if count != 1 {
		return nil
	}
	return found
}

// --- <select> ---

// isSelected reports whether an <option> tag's attributes include the bare
// "selected" flag, which parseAttrs doesn't see.
func isSelected(attrs string) bool {
	return selectedFlag.MatchString(quotedRe.ReplaceAllString(attrs, ""))
}

func stripTags(s string) string { return cleanText(tagRe.ReplaceAllString(s, " ")) }

// fieldName turns a form field name into an option name. structured reports
// that the name is the kind that carries an option, so it needs no known word.
func fieldName(name string) (opt string, structured bool) {
	name = strings.TrimSpace(name)
	if m := bracketName.FindStringSubmatch(name); m != nil {
		return titleName(m[1]), true
	}
	low := strings.ToLower(name)
	for _, p := range structuredPrefixes {
		if strings.HasPrefix(low, p) {
			return titleName(name[len(p):]), true
		}
	}
	if strings.HasPrefix(low, "dwvar_") {
		return titleName(name[strings.LastIndex(name, "_")+1:]), true
	}
	return titleName(name), false
}

func anyWord(name string, set map[string]bool) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if set[w] {
			return true
		}
	}
	return false
}

func selectOptions(set *optionSet, src string) {
	labels := map[string]string{}
	for _, m := range labelRe.FindAllStringSubmatch(src, -1) {
		if id := parseAttrs(m[1])["for"]; id != "" {
			if t := strings.TrimRight(strings.TrimSpace(stripTags(m[2])), ":*"); t != "" && len(t) <= 40 {
				labels[id] = strings.TrimSpace(t)
			}
		}
	}
	for _, m := range selectRe.FindAllStringSubmatch(src, -1) {
		a := parseAttrs(m[1])
		field, structured := fieldName(a["name"])
		label := firstNonEmpty(labels[a["id"]], a["aria-label"])
		name := firstNonEmpty(label, field)
		if name == "" || anyWord(name, notOptionWords) || anyWord(field, notOptionWords) {
			continue
		}
		if !structured && !anyWord(name, optionWords) && !anyWord(field, optionWords) {
			continue
		}
		o := set.get(name)
		o.addField(field)
		var selected string
		for _, om := range optionTagRe.FindAllStringSubmatch(m[2], -1) {
			oa := parseAttrs(om[1])
			text := cleanText(om[2])
			if text == "" {
				text = oa["label"]
			}
			// A placeholder has an empty value or says "Choose a size".
			if _, hasValue := oa["value"]; text == "" || (hasValue && oa["value"] == "") || placeholderRe.MatchString(text) {
				continue
			}
			o.add(text, oa["value"])
			if isSelected(om[1]) && selected == "" {
				selected = text
			}
		}
		if selected != "" {
			o.pick(selected, false)
		}
	}
}

// --- the link's query ---

func queryOptions(set *optionSet, base string) {
	u, err := url.Parse(base)
	if err != nil || u.RawQuery == "" {
		return
	}
	q := u.Query()
	for _, key := range queryKeys(u.RawQuery) {
		vals := q[key]
		if len(vals) != 1 || strings.TrimSpace(vals[0]) == "" {
			continue
		}
		name, structured := fieldName(key)
		low := strings.ToLower(key)
		if !structured && low != "size" && low != "color" && low != "colour" { //nolint:misspell // shops spell it both ways
			continue
		}
		// The link only fills or picks an option the page names.
		if anyWord(name, notOptionWords) {
			continue
		}
		o := set.byField(name)
		if o == nil {
			o = set.find(name)
		}
		if o != nil {
			// Only a structured key can give a value the page doesn't list.
			o.pickLink(vals[0], structured)
		}
	}
}

// queryKeys gives a query's keys once each, in link order, so the result is the
// same every time and doesn't depend on map order.
func queryKeys(raw string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, part := range strings.Split(raw, "&") {
		k, _, _ := strings.Cut(part, "=")
		k, err := url.QueryUnescape(k)
		if err != nil || k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	return keys
}
