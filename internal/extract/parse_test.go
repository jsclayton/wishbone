package extract

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		file string
		want Product
	}{
		{"jsonld_product.html", Product{
			Title:    "Trail Runner Shoes & Socks",
			Images:   []string{"https://cdn.example.com/a.jpg", "https://example.com/img/b.jpg"},
			Price:    "89.50",
			Currency: "USD",
		}},
		{"jsonld_graph.html", Product{
			Title:    "Espresso Machine",
			Images:   []string{"https://cdn.example.com/espresso.jpg"},
			Price:    "1299",
			Currency: "EUR",
		}},
		{"opengraph.html", Product{
			Title:    "Cast Iron Skillet",
			Images:   []string{"https://cdn.example.com/skillet.jpg"},
			Price:    "1024.00",
			Currency: "GBP",
		}},
		{"meta_only.html", Product{
			Title:    "Wool Blanket | Store",
			Images:   []string{"https://example.com/blanket.png"},
			Price:    "45.99",
			Currency: "EUR",
		}},
		// Several pictures, best first, with no repeats and none without a host.
		{"jsonld_many_pictures.html", Product{
			Title: "Crew Sweater",
			Images: []string{
				"https://cdn.example.com/sweater-black.jpg", "https://cdn.example.com/sweater-navy.jpg",
				"https://example.com/img/sweater-green.jpg", "https://cdn.example.com/sweater-red.jpg",
				"https://cdn.example.com/sweater-gray.jpg", "https://cdn.example.com/sweater-cream.jpg",
			},
			Price:    "65.00",
			Currency: "USD",
		}},
		{"nothing.html", Product{}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			body, err := os.ReadFile("testdata/" + tt.file)
			require.NoError(t, err)
			require.Equal(t, tt.want, Parse(body, "https://example.com/products/1"))
		})
	}
}

func TestParseBadJSONLDFallsBack(t *testing.T) {
	html := `<meta property="og:title" content="OG"><script type="application/ld+json">{not json</script>`
	got := Parse([]byte(html), "https://example.com/")
	require.Equal(t, "OG", got.Title)
}

func TestParseMixesSources(t *testing.T) {
	html := `<script type="application/ld+json">{"@type":"Product","name":"LD Name"}</script>
<meta property="og:title" content="OG Name"><meta property="og:image" content="https://x.test/i.jpg">
<meta property="product:price:amount" content="5"><meta property="product:price:currency" content="usd">`
	got := Parse([]byte(html), "https://example.com/")
	want := Product{Title: "LD Name", Images: []string{"https://x.test/i.jpg"}, Price: "5", Currency: "USD"}
	require.Equal(t, want, got)
}

func TestParseSignal(t *testing.T) {
	tests := map[string]bool{
		`<title>Robot or human?</title>`:                                                    false,
		`<meta name="twitter:title" content="x"><title>y</title>`:                           false,
		`<meta property="og:title" content="x">`:                                            true,
		`<meta property="og:image" content="/i.jpg">`:                                       true,
		`<meta itemprop="price" content="5">`:                                               true,
		`<script type="application/ld+json">{"@type":"Product"}</script>`:                   true,
		`<script type="application/ld+json">{"@type":"schema:Product"}</script>`:            true,
		`<script type="application/ld+json">{"@type":"http://schema.org/Product"}</script>`: true,
		`<script type="application/ld+json">{"@type":"Organization"}</script>`:              false,
	}
	for html, want := range tests {
		_, got := parse([]byte(html), "https://example.com/")
		require.Equal(t, want, got, html)
	}
}

func TestParseDropsPictureWithNoHost(t *testing.T) {
	html := `<script type="application/ld+json">{"@type":"Product","name":"Dog","image":["https:files/x.jpg"]}</script>
<meta property="og:image" content="https://cdn.example.com/good.jpg">`
	got := Parse([]byte(html), "https://example.com/products/1")
	require.Equal(t, []string{"https://cdn.example.com/good.jpg"}, got.Images)
}
