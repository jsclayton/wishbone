package extract

// The helpers below share the parser's HTML scanning with other packages,
// such as the LLM stage, so the two can't drift apart.

// ParseAttrs returns the lowercase-named, unescaped attributes of one HTML tag.
func ParseAttrs(tag string) map[string]string { return parseAttrs(tag) }

// PageTitle returns the cleaned text of the page's <title>, or "".
func PageTitle(page string) string { return cleanText(firstMatch(titleRe, page)) }

// ResolveAll resolves refs against base. It keeps absolute http(s) URLs only,
// drops data: URIs and duplicates, and keeps order.
func ResolveAll(refs []string, base string) []string { return resolveAll(refs, base) }
