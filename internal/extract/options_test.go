package extract

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func parseFixture(t *testing.T, file, base string) Product {
	t.Helper()
	body, err := os.ReadFile("testdata/" + file)
	require.NoError(t, err)
	return Parse(body, base)
}

func TestOptionsProductGroupListsVariantValues(t *testing.T) {
	got := parseFixture(t, "options_productgroup.html", "https://shop.example/hoodie")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"S", "M"}},
		{Name: "Color", Values: []string{"Black", "Navy"}},
	}, got.Options)
}

func TestOptionsLinkSelectsAVariant(t *testing.T) {
	got := parseFixture(t, "options_productgroup.html", "https://shop.example/hoodie?variant=104")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"S", "M"}, Selected: "M"},
		{Name: "Color", Values: []string{"Black", "Navy"}, Selected: "Navy"},
	}, got.Options)
}

func TestOptionsLinkQuerySelectsAValue(t *testing.T) {
	got := parseFixture(t, "options_productgroup.html", "https://shop.example/hoodie?size=m&utm_source=x")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"S", "M"}, Selected: "M"},
		{Name: "Color", Values: []string{"Black", "Navy"}},
	}, got.Options)
}

func TestOptionsNamesOnly(t *testing.T) {
	got := parseFixture(t, "options_names_only.html", "https://shop.example/hoodie")
	require.Equal(t, []Option{{Name: "Size"}, {Name: "Color"}}, got.Options)
}

func TestOptionsSelectElements(t *testing.T) {
	got := parseFixture(t, "options_select.html", "https://shop.example/runner")
	require.Equal(t, []Option{
		{Name: "Shoe size", Values: []string{"8", "9", "10"}, Selected: "9"},
		{Name: "Color", Values: []string{"Natural Black", "Tree Green"}},
	}, got.Options)
}

func TestOptionsQueryAttributeSelectsSelectValue(t *testing.T) {
	// WooCommerce style: the link names the option and the value's slug.
	got := parseFixture(t, "options_select.html", "https://shop.example/runner?attribute_pa_color=tree-green")
	require.Equal(t, "Tree Green", got.Options[1].Selected)
	require.Equal(t, "9", got.Options[0].Selected)
}

func TestOptionsConflictSelectsNothing(t *testing.T) {
	// The page marks 9 as selected, and the link says 10.
	got := parseFixture(t, "options_conflict.html", "https://shop.example/runner?options%5BSize%5D=10")
	require.Equal(t, []Option{{Name: "Size", Values: []string{"8", "9", "10"}}}, got.Options)
}

func TestOptionsAgreeingSourcesSelect(t *testing.T) {
	got := parseFixture(t, "options_conflict.html", "https://shop.example/runner?options%5BSize%5D=9")
	require.Equal(t, "9", got.Options[0].Selected)
}

func TestOptionsProductOwnColorAndSize(t *testing.T) {
	got := parseFixture(t, "options_product_own.html", "https://shop.example/tote")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"Large"}, Selected: "Large"},
		{Name: "Color", Values: []string{"Natural"}, Selected: "Natural"},
	}, got.Options)
}

func TestOptionsNoneFound(t *testing.T) {
	for _, f := range []string{"jsonld_product.html", "opengraph.html", "meta_only.html", "nothing.html", "jsonld_many_pictures.html"} {
		require.Empty(t, parseFixture(t, f, "https://example.com/p?size=m").Options, f)
	}
}

func TestOptionsLimits(t *testing.T) {
	var sel string
	for i := 0; i < 6; i++ {
		sel += fmt.Sprintf(`<select name="options[Opt%d]">`, i)
		for j := 0; j < 40; j++ {
			sel += fmt.Sprintf(`<option>v%d</option>`, j)
		}
		sel += `</select>`
	}
	got := Parse([]byte(sel), "https://example.com/")
	require.Len(t, got.Options, MaxOptions)
	for _, o := range got.Options {
		require.Len(t, o.Values, MaxOptionValues)
	}
}

func TestOptionsSkipSelectsThatAreNotChoices(t *testing.T) {
	html := `<select name="quantity"><option>1</option></select>
<select name="sort_by"><option>Newest</option></select>
<select name="currency"><option>USD</option></select>
<select name="options[Size]"><option value="">Select</option></select>`
	got := Parse([]byte(html), "https://example.com/")
	require.Equal(t, []Option{{Name: "Size"}}, got.Options)
}

func TestOptionsSelectedMustBeListed(t *testing.T) {
	// The page lists S and M, so a link that says xxl picks nothing.
	html := `<select name="options[Size]"><option>S</option><option>M</option></select>`
	got := Parse([]byte(html), "https://example.com/p?options[Size]=xxl")
	require.Equal(t, []Option{{Name: "Size", Values: []string{"S", "M"}}}, got.Options)
}

func TestOptionsLinkValueTheGroupDoesNotList(t *testing.T) {
	// Shops often put a color code or an id in color=. It isn't a value to show.
	for _, q := range []string{"?color=001", "?size=XXL"} {
		got := parseFixture(t, "options_productgroup.html", "https://shop.example/hoodie"+q)
		require.Equal(t, []Option{
			{Name: "Size", Values: []string{"S", "M"}},
			{Name: "Color", Values: []string{"Black", "Navy"}},
		}, got.Options, q)
	}
}

func TestOptionsStructuredLinkFillsAnOptionWithNoValues(t *testing.T) {
	got := parseFixture(t, "options_names_only.html", "https://shop.example/hoodie?attribute_pa_size=m&color=blue")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"m"}, Selected: "m"},
		{Name: "Color"},
	}, got.Options)
}

func TestOptionsSeparateOptionsWithTheSameLastWord(t *testing.T) {
	html := `<label for="b">Band size</label><select id="b" name="options[Band]"><option>32</option><option>34</option></select>
<label for="c">Cup size</label><select id="c" name="options[Cup]"><option>A</option><option>B</option></select>
<label for="f">Frame color</label><select id="f" name="options[Frame]"><option>Black</option></select>
<label for="l">Lens color</label><select id="l" name="options[Lens]"><option>Grey</option></select>`
	got := Parse([]byte(html), "https://example.com/p")
	require.Equal(t, []Option{
		{Name: "Band size", Values: []string{"32", "34"}},
		{Name: "Cup size", Values: []string{"A", "B"}},
		{Name: "Frame color", Values: []string{"Black"}},
		{Name: "Lens color", Values: []string{"Grey"}},
	}, got.Options)
}

func TestOptionsCamelCaseVariesBy(t *testing.T) {
	got := parseFixture(t, "options_productgroup_gender.html", "https://shop.example/tee?variant=T-W-M")
	require.Equal(t, []Option{
		{Name: "Suggested gender", Values: []string{"Women", "Men"}, Selected: "Women"},
		{Name: "Size", Values: []string{"S", "M"}, Selected: "M"},
	}, got.Options)
}

func TestOptionsTheLinkAloneGivesNoOptions(t *testing.T) {
	// A link names no option the page doesn't offer. Its keys are often ids or tracking values.
	for _, q := range []string{"?option_id=7&pa_ref=x", "?attribute_country=US", "?options%5BQuantity%5D=2", "?attribute_pa_color=natural-black"} {
		require.Empty(t, parseFixture(t, "jsonld_product.html", "https://example.com/p"+q).Options, q)
	}
}

func TestOptionsTheLinkSkipsWordsThatAreNotOptions(t *testing.T) {
	// "Page size" ends in "size", but it is how many results a page shows.
	html := `<select name="options[Size]"><option>S</option><option>M</option></select>`
	got := Parse([]byte(html), "https://example.com/p?pa_page_size=m")
	require.Equal(t, []Option{{Name: "Size", Values: []string{"S", "M"}}}, got.Options)
}

func TestOptionsFromTheLinkKeepPageOrder(t *testing.T) {
	// The page names five options in one order and the link fills them in another.
	html := `<select name="options[Width]"></select><select name="options[Edition]"></select>
<select name="options[Fit]"></select><select name="options[Color]"></select><select name="options[Size]"></select>`
	link := "https://example.com/p?attribute_pa_size=m&attribute_pa_color=red&attribute_pa_fit=slim&attribute_pa_edition=gold&attribute_pa_width=wide"
	for range 30 {
		got := Parse([]byte(html), link)
		require.Equal(t, []Option{
			{Name: "Width", Values: []string{"wide"}, Selected: "wide"},
			{Name: "Edition", Values: []string{"gold"}, Selected: "gold"},
			{Name: "Fit", Values: []string{"slim"}, Selected: "slim"},
			{Name: "Color", Values: []string{"red"}, Selected: "red"},
		}, got.Options)
	}
}

func TestOptionsAOneWordOptionKeepsTwoNamesApart(t *testing.T) {
	// JSON-LD says Size, and the page has a Band size and a Cup size. Size can't be both.
	body, err := os.ReadFile("testdata/options_productgroup.html")
	require.NoError(t, err)
	page := string(body) + `<label for="b">Band size</label><select id="b" name="options[Band]"><option>32</option><option>34</option></select>
<label for="c">Cup size</label><select id="c" name="options[Cup]"><option>A</option><option>B</option></select>`
	got := Parse([]byte(page), "https://shop.example/hoodie?size=m")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"S", "M"}, Selected: "M"},
		{Name: "Color", Values: []string{"Black", "Navy"}},
		{Name: "Band size", Values: []string{"32", "34"}},
		{Name: "Cup size", Values: []string{"A", "B"}},
	}, got.Options)
}

func TestOptionsAOneWordOptionJoinsOneLongerName(t *testing.T) {
	body, err := os.ReadFile("testdata/options_productgroup.html")
	require.NoError(t, err)
	page := string(body) + `<label for="s">Hoodie size</label><select id="s" name="options[Size]"><option>S</option><option selected>M</option><option>L</option></select>`
	got := Parse([]byte(page), "https://shop.example/hoodie")
	require.Equal(t, []Option{
		{Name: "Size", Values: []string{"S", "M", "L"}, Selected: "M"},
		{Name: "Color", Values: []string{"Black", "Navy"}},
	}, got.Options)
}

func TestOptionsLinkFieldNameSelectsADropdownWithAnotherLabel(t *testing.T) {
	html := `<label for="b">Band size</label><select id="b" name="options[Band]"><option>32</option><option>34</option></select>`
	got := Parse([]byte(html), "https://example.com/p?options%5BBand%5D=32")
	require.Equal(t, []Option{{Name: "Band size", Values: []string{"32", "34"}, Selected: "32"}}, got.Options)
}

func TestOptionsConflictingLinkValuesSelectNothing(t *testing.T) {
	got := parseFixture(t, "options_names_only.html", "https://shop.example/hoodie?attribute_pa_size=m&options%5BSize%5D=l")
	require.Equal(t, "Size", got.Options[0].Name)
	require.Empty(t, got.Options[0].Values)
	require.Empty(t, got.Options[0].Selected)
}

func TestOptionsAgreeingLinkValuesSelect(t *testing.T) {
	got := parseFixture(t, "options_names_only.html", "https://shop.example/hoodie?attribute_pa_size=m&options%5BSize%5D=M")
	require.Equal(t, Option{Name: "Size", Values: []string{"m"}, Selected: "m"}, got.Options[0])
}
