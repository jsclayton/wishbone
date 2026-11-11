package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/llm"
	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/netguard/netguardtest"
	"jsclayton.dev/wishbone/internal/store"
)

type fakeFetcher struct {
	res   extract.Result
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (extract.Result, error) {
	f.calls++
	return f.res, f.err
}

type fakeFiller struct {
	fill  func(extract.Product) extract.Product
	usage llm.Usage
	err   error
	calls int
	got   extract.Product
	page  []byte
}

func (f *fakeFiller) Fill(_ context.Context, p extract.Product, page []byte, _ string) (extract.Product, llm.Usage, error) {
	f.calls++
	f.got, f.page = p, page
	if f.err != nil {
		return p, f.usage, f.err
	}
	if f.fill != nil {
		p = f.fill(p)
	}
	return p, f.usage, nil
}

func okResult(title string) extract.Result {
	return extract.Result{
		Product: extract.Product{Title: title, Price: "10.00", Currency: "USD", Images: []string{"https://s.test/i.jpg"}},
		URL:     "https://s.test/p", Store: "s.test", Status: extract.StatusOK,
	}
}

type env struct {
	svc     *Service
	reg     *prometheus.Registry
	direct  *fakeFetcher
	bd      *fakeFetcher
	filler  *fakeFiller
	clock   *time.Time
	options Options
}

func newEnv(t *testing.T, mutate func(*Options)) *env {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))

	e := &env{
		direct: &fakeFetcher{res: okResult("Direct")},
		bd:     &fakeFetcher{res: okResult("BD")},
		filler: &fakeFiller{},
		reg:    prometheus.NewRegistry(),
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.clock = &now
	e.options = Options{
		DB:         st.DB(),
		Direct:     e.direct,
		BrightData: e.bd,
		LLM:        e.filler,
		Now:        func() time.Time { return *e.clock },
		Registerer: e.reg,
	}
	e.options.BrightDataRequests = func() int64 { return int64(e.bd.calls) }
	if mutate != nil {
		mutate(&e.options)
	}
	e.svc = New(e.options)
	return e
}

func counter(t *testing.T, e *env, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := e.reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if want, ok := labels[l.GetName()]; ok && want != l.GetValue() {
					continue next
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

func stageCount(t *testing.T, e *env, stage, outcome string) float64 {
	return counter(t, e, "wishbone_extractions_total", map[string]string{"stage": stage, "outcome": outcome})
}

func TestDirectSuccessStopsThere(t *testing.T) {
	e := newEnv(t, nil)
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p?utm_source=x")
	require.NoError(t, err)
	require.Equal(t, "Direct", res.Title)
	require.Equal(t, StageDirect, res.Stage)
	require.True(t, res.Found())
	require.Equal(t, 1, e.direct.calls)
	require.Zero(t, e.bd.calls)
	require.Zero(t, e.filler.calls, "a complete product needs no LLM")
	require.Equal(t, 1.0, stageCount(t, e, "direct", "success"))
}

func TestFallsThroughOnErrorBlockedAndNoData(t *testing.T) {
	cases := map[string]func(f *fakeFetcher){
		"error":   func(f *fakeFetcher) { f.err = errors.New("boom") },
		"blocked": func(f *fakeFetcher) { f.res = extract.Result{Status: extract.StatusBlocked} },
		"nodata":  func(f *fakeFetcher) { f.res = extract.Result{Status: extract.StatusNoData} },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			set(e.direct)
			res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
			require.NoError(t, err)
			require.Equal(t, "BD", res.Title)
			require.Equal(t, StageBrightData, res.Stage)
			require.Equal(t, 1.0, stageCount(t, e, "direct", "failure"))
			require.Equal(t, 1.0, stageCount(t, e, "brightdata", "success"))
			require.Equal(t, 1.0, counter(t, e, "wishbone_brightdata_requests_total", nil))
		})
	}
}

func TestPrivateAddressRefusalStopsThePipeline(t *testing.T) {
	refusal := fmt.Errorf("fetch http://192.168.1.1/admin: %w", &url.Error{
		Op: "Get", URL: "http://192.168.1.1/admin",
		Err: &net.OpError{Op: "dial", Err: netguard.ErrPrivateAddress},
	})
	e := newEnv(t, nil)
	e.direct.err = refusal
	e.direct.res = extract.Result{}
	res, err := e.svc.Extract(context.Background(), 1, "http://192.168.1.1/admin")
	require.NoError(t, err)
	require.False(t, res.Found())
	require.Equal(t, "http://192.168.1.1/admin", res.URL)
	require.Equal(t, 1, e.direct.calls)
	require.Zero(t, e.bd.calls, "Bright Data must not receive a private address")
	require.Zero(t, e.filler.calls, "the LLM must not run")
	require.Equal(t, 1.0, stageCount(t, e, "direct", "failure"))
	require.Zero(t, counter(t, e, "wishbone_brightdata_requests_total", nil))
}

func TestPrivateHostNameStopsThePipelineThroughRealDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	// localhost resolves to a loopback address, as a private host name would.
	target := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	e := newEnv(t, func(o *Options) { o.Direct = extract.NewDirect(extract.Options{}) })
	res, err := e.svc.Extract(context.Background(), 1, target)
	require.NoError(t, err)
	require.False(t, res.Found())
	require.Zero(t, e.bd.calls)
	require.Zero(t, e.filler.calls)
}

// dialFetcher is a direct stage that behaves like the real Direct: an
// http.Client over netguard.DialContext, with the host name resolved by a fake
// DNS.
type dialFetcher struct{ client *http.Client }

func (f dialFetcher) Fetch(ctx context.Context, rawURL string) (extract.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return extract.Result{}, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return extract.Result{}, err
	}
	_ = resp.Body.Close()
	return extract.Result{Status: extract.StatusNoData}, nil
}

// A name whose first address gives no answer and whose second is private must
// end the pipeline. If the first address's timeout hid the refusal, the link
// would go on to Bright Data. The netguard tests cover both orders;
// this one runs the broken order only, because a stalled address costs seconds.
func TestMixedAddressNameWithPrivateAddressStopsThePipeline(t *testing.T) {
	// 192.0.2.1 is TEST-NET-1: never routed, so a dial to it fails or stalls.
	// The test assumes the network doesn't intercept it. A VPN or transparent
	// proxy that answers every TCP connection would break it.
	addrs := []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("10.0.0.1")}
	dialer := &net.Dialer{
		Timeout:  3 * time.Second,
		Resolver: netguardtest.Resolver(t, map[string][]netip.Addr{"mixed.test": addrs}),
	}
	tr := &http.Transport{DialContext: netguard.DialContext(dialer)}
	t.Cleanup(tr.CloseIdleConnections)
	e := newEnv(t, func(o *Options) { o.Direct = dialFetcher{client: &http.Client{Transport: tr}} })
	res, err := e.svc.Extract(context.Background(), 1, "http://mixed.test/p")
	require.NoError(t, err)
	require.False(t, res.Found())
	require.Zero(t, e.bd.calls, "Bright Data must not receive the link")
	require.Zero(t, e.filler.calls, "the LLM must not run")
	require.Equal(t, 1.0, stageCount(t, e, "direct", "failure"))
	require.Zero(t, counter(t, e, "wishbone_brightdata_requests_total", nil))
}

func TestDNSNotFoundFromDirectStopsThePipeline(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.err = fmt.Errorf("fetch http://nas.lan/p: %w", &url.Error{
		Op: "Get", URL: "http://nas.lan/p",
		Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "nas.lan", IsNotFound: true}},
	})
	e.direct.res = extract.Result{}
	res, err := e.svc.Extract(context.Background(), 1, "http://nas.lan/p")
	require.NoError(t, err)
	require.False(t, res.Found())
	require.Equal(t, 1, e.direct.calls)
	require.Zero(t, e.bd.calls, "Bright Data must not receive an unresolvable host")
	require.Zero(t, e.filler.calls, "the LLM must not run")
	require.Equal(t, 1.0, stageCount(t, e, "direct", "failure"))
}

func TestDNSTimeoutFromDirectStillRunsBrightData(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.err = &net.OpError{Op: "dial", Err: &net.DNSError{Err: "i/o timeout", Name: "s.test", IsTimeout: true}}
	e.bd.res = okResult("From Bright Data")
	res, err := e.svc.Extract(context.Background(), 1, "http://s.test/p")
	require.NoError(t, err)
	require.Equal(t, StageBrightData, res.Stage)
	require.Equal(t, 1, e.bd.calls)
}

func TestDNSNotFoundFromBrightDataStillRunsTheLLM(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: textPage(200)}
	e.bd.res, e.bd.err = extract.Result{}, &net.DNSError{Err: "no such host", Name: "s.test", IsNotFound: true}
	e.filler.fill = func(p extract.Product) extract.Product { p.Title = "From LLM"; return p }
	res, err := e.svc.Extract(context.Background(), 1, "http://s.test/p")
	require.NoError(t, err)
	require.Equal(t, 1, e.bd.calls)
	require.Equal(t, 1, e.filler.calls)
	require.Equal(t, StageLLM, res.Stage)
}

func TestUnresolvableHostStopsThePipelineThroughRealDirect(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://gone.invalid/p", http.StatusFound)
	}))
	defer redirect.Close()
	cases := map[string]struct {
		target string
		opts   extract.Options
	}{
		// The .invalid cases assume the machine's resolver answers NXDOMAIN.
		"direct link":         {target: "http://nas.invalid/p"},
		"decimal address":     {target: "http://3232235777/p"},
		"short dotted":        {target: "http://192.168.1/p"},
		"redirect to unknown": {target: redirect.URL, opts: extract.Options{AllowPrivateAddrs: true}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, func(o *Options) { o.Direct = extract.NewDirect(c.opts) })
			res, err := e.svc.Extract(context.Background(), 1, c.target)
			require.NoError(t, err)
			require.False(t, res.Found())
			require.Zero(t, e.bd.calls, "Bright Data must not receive the link")
			require.Zero(t, e.filler.calls, "the LLM must not run")
		})
	}
}

func TestSkipsUnconfiguredStages(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Direct = nil })
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, StageBrightData, res.Stage)

	e = newEnv(t, func(o *Options) { o.BrightData = nil })
	e.direct.res = extract.Result{Status: extract.StatusBlocked, Body: []byte("<html/>")}
	e.filler.fill = func(p extract.Product) extract.Product { p.Title = "From LLM"; return p }
	res, err = e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, "From LLM", res.Title)
	require.Equal(t, StageLLM, res.Stage)
	require.Zero(t, e.bd.calls)
	require.Equal(t, 0.0, counter(t, e, "wishbone_brightdata_requests_total", nil))
}

func TestNoStagesConfigured(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Direct, o.BrightData, o.LLM = nil, nil, nil })
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p?utm_source=x")
	require.NoError(t, err)
	require.False(t, res.Found())
	require.Equal(t, "https://s.test/p", res.URL)
}

func TestAllStagesFailGivesBlankResultAndIsNotCached(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.err = errors.New("boom")
	e.bd.err = errors.New("boom")
	e.filler.err = errors.New("boom")
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.False(t, res.Found())
	require.Equal(t, "https://s.test/p", res.URL)
	require.Equal(t, "s.test", res.Store)

	_, err = e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, 2, e.direct.calls, "a failure is not cached")
}

func TestLLMRunsAfterAllFetchersFailWithBodyAndDropsNoDataTitle(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: []byte("<html><script>direct</script></html>"),
		Product: extract.Product{Title: "Robot or human?"}}
	e.bd.res = extract.Result{Status: extract.StatusNoData, Body: []byte("<html><script>bd</script></html>"),
		Product: extract.Product{Title: "Robot or human?"}}
	e.filler.fill = func(p extract.Product) extract.Product { p.Title = "Real"; p.Price = "5.00"; return p }
	e.filler.usage = llm.Usage{InputTokens: 100, OutputTokens: 20}

	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, "Real", res.Title)
	require.Equal(t, StageLLM, res.Stage)
	require.Empty(t, e.filler.got.Title, "the no-data title is not passed to the LLM")
	require.Equal(t, "<html><script>bd</script></html>", string(e.filler.page), "pages with equal text tie, so the later page is used")
	require.Equal(t, 1.0, stageCount(t, e, "llm", "success"))
	require.Equal(t, 100.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "input"}))
	require.Equal(t, 20.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "output"}))
}

type fixedProvider struct{ text string }

func (p fixedProvider) Complete(context.Context, string, string) (llm.Completion, error) {
	return llm.Completion{Text: p.text}, nil
}

func (p fixedProvider) CompleteImage(context.Context, string, string, llm.Image) (llm.Completion, error) {
	return llm.Completion{Text: p.text}, nil
}

func TestBlankLLMTitleUsesTheFallbackTitle(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.LLM = &llm.Extractor{Provider: fixedProvider{text: `{"title":"\u200b","price":"5.00","currency":"USD"}`}}
	})
	body := []byte("<html><head><title>Robot or human?</title></head><body>" + strings.Repeat("word ", 50) + "</body></html>")
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: body, URL: "https://s.test/p",
		Product: extract.Product{Title: "Robot or human?"}}
	e.bd.res = e.direct.res

	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, StageLLM, res.Stage)
	require.Equal(t, "Robot or human?", res.Title, "a blank LLM title leaves the fallback title")
}

// textPage is a page with n words of visible text.
func textPage(n int) []byte {
	return []byte("<html><body><p>" + strings.Repeat("word ", n) + "</p></body></html>")
}

func TestLLMReadsDirectPageOverThinBrightDataPage(t *testing.T) {
	for name, bdStatus := range map[string]extract.Status{"no data": extract.StatusNoData, "blocked": extract.StatusBlocked} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.direct.res = extract.Result{Status: extract.StatusNoData, Body: textPage(200), URL: "https://s.test/direct", Store: "s.test",
				Product: extract.Product{Title: "Direct page"}}
			e.bd.res = extract.Result{Status: bdStatus, Body: []byte("<html><body>Robot or human?</body></html>"), URL: "https://bd.test/p", Store: "bd.test",
				Product: extract.Product{Title: "Robot or human?"}}
			e.filler.fill = func(p extract.Product) extract.Product { p.Price = "5.00"; return p }

			res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
			require.NoError(t, err)
			require.Equal(t, string(textPage(200)), string(e.filler.page), "the LLM reads the page with more text")
			require.Equal(t, "https://s.test/direct", res.URL, "the URL comes from the page the LLM read")
			require.Equal(t, "s.test", res.Store)
			require.Equal(t, "Direct page", res.Title, "the fallback title comes from the page the LLM read")
			require.Equal(t, 1.0, stageCount(t, e, "brightdata", "failure"), "a thin Bright Data page is still a failure")
		})
	}
}

func TestFallbackTitleComesFromTheLongerPageWithoutLLM(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.LLM = nil })
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: textPage(200),
		Product: extract.Product{Title: "Direct page"}}
	e.bd.res = extract.Result{Status: extract.StatusNoData, Body: []byte("<html><body>Robot or human?</body></html>"),
		Product: extract.Product{Title: "Robot or human?"}}
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, "Direct page", res.Title)
}

func TestLLMReadsBrightDataPageWithMoreText(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: []byte("<html><body>Loading</body></html>")}
	e.bd.res = extract.Result{Status: extract.StatusNoData, Body: textPage(200)}
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, string(textPage(200)), string(e.filler.page))
}

func TestLLMComparesTextNotPageSize(t *testing.T) {
	e := newEnv(t, nil)
	scripts := "<html><body>Shell<script>" + strings.Repeat("var a = 1;", 2000) + "</script></body></html>"
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: []byte(scripts)}
	e.bd.res = extract.Result{Status: extract.StatusNoData, Body: textPage(50)}
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, string(textPage(50)), string(e.filler.page), "a page of scripts does not win on size")
}

func TestLLMComparesTextWithoutACap(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: textPage(2000)}
	e.bd.res = extract.Result{Status: extract.StatusNoData, Body: textPage(1500)}
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, string(textPage(2000)), string(e.filler.page), "two pages over the LLM limit don't tie")
}

func TestEmptyBrightDataBodyKeepsDirectPageWithoutText(t *testing.T) {
	e := newEnv(t, nil)
	direct := "<html><script>x</script></html>"
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: []byte(direct)}
	e.bd.res = extract.Result{Status: extract.StatusBlocked}
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, direct, string(e.filler.page), "an empty body doesn't tie with a page that has no text")
}

func TestEmptyBrightDataBodyKeepsDirectPage(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: textPage(20)}
	e.bd.res = extract.Result{Status: extract.StatusBlocked}
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, string(textPage(20)), string(e.filler.page))
}

func TestNoDataTitleIsLastResortFallback(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.BrightData = nil })
	e.direct.res = extract.Result{Status: extract.StatusNoData, Body: []byte("<html/>"), Product: extract.Product{Title: "Shop page"}}
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, "Shop page", res.Title)
	require.Equal(t, 1.0, stageCount(t, e, "llm", "failure"))
}

func TestLLMFillsMissingFieldsAfterSuccessfulFetch(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusOK, URL: "https://s.test/p", Body: []byte("<html/>"),
		Product: extract.Product{Title: "Widget"}}
	e.filler.fill = func(p extract.Product) extract.Product { p.Price, p.Currency = "9.00", "USD"; return p }
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, "Widget", res.Title)
	require.Equal(t, "9.00", res.Price)
	require.Equal(t, "Widget", e.filler.got.Title)
	require.Zero(t, e.bd.calls, "Bright Data only runs when direct fails")
	require.Equal(t, 1.0, stageCount(t, e, "direct", "success"))
	require.Equal(t, 1.0, stageCount(t, e, "llm", "success"))
}

func TestLLMErrorKeepsFetchedProduct(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusOK, URL: "https://s.test/p", Body: []byte("<html/>"),
		Product: extract.Product{Title: "Widget"}}
	e.filler.err = errors.New("boom")
	e.filler.usage = llm.Usage{InputTokens: 3}
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, "Widget", res.Title)
	require.Equal(t, 1.0, stageCount(t, e, "llm", "failure"))
	require.Equal(t, 3.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "input"}))
}

func TestUsageReturnedWithAnLLMErrorReachesTheTokenMetric(t *testing.T) {
	e := newEnv(t, nil)
	e.direct.res = extract.Result{Status: extract.StatusOK, URL: "https://s.test/p", Body: []byte("<html/>"),
		Product: extract.Product{Title: "Widget"}}
	e.filler.err = errors.New("llm: anthropic returned no text")
	e.filler.usage = llm.Usage{InputTokens: 30, OutputTokens: 8}
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, 30.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "input"}))
	require.Equal(t, 8.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "output"}))
}

func TestCacheHitAndExpiry(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	first, err := e.svc.Extract(ctx, 1, "https://s.test/p?utm_source=a")
	require.NoError(t, err)
	require.False(t, first.Cached)

	*e.clock = e.clock.Add(24*time.Hour - time.Second)
	second, err := e.svc.Extract(ctx, 2, "https://S.test/p?utm_medium=b&fbclid=1")
	require.NoError(t, err)
	require.True(t, second.Cached)
	require.Equal(t, first.Title, second.Title)
	require.Equal(t, first.Images, second.Images)
	require.Equal(t, 1, e.direct.calls)

	*e.clock = e.clock.Add(2 * time.Second)
	e.direct.res = okResult("Fresh")
	third, err := e.svc.Extract(ctx, 1, "https://s.test/p")
	require.NoError(t, err)
	require.False(t, third.Cached)
	require.Equal(t, "Fresh", third.Title)
	require.Equal(t, 2, e.direct.calls)

	var n int
	require.NoError(t, e.options.DB.Get(&n, "SELECT COUNT(*) FROM extraction_cache"))
	require.Equal(t, 1, n, "expired rows are replaced")
}

func TestBypassCacheRunsStagesAndRefreshesCache(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	_, err := e.svc.Extract(ctx, 1, "https://s.test/p")
	require.NoError(t, err)

	e.direct.res = okResult("Fresh")
	res, err := e.svc.Extract(ctx, 1, "https://s.test/p", BypassCache())
	require.NoError(t, err)
	require.False(t, res.Cached)
	require.Equal(t, "Fresh", res.Title)
	require.Equal(t, 2, e.direct.calls)

	res, err = e.svc.Extract(ctx, 1, "https://s.test/p")
	require.NoError(t, err)
	require.True(t, res.Cached)
	require.Equal(t, "Fresh", res.Title)
}

func TestCacheUsesUTC(t *testing.T) {
	zone := time.FixedZone("X", 5*3600)
	e := newEnv(t, func(o *Options) {
		o.Now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, zone) }
	})
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	var created string
	require.NoError(t, e.options.DB.Get(&created, "SELECT created_at FROM extraction_cache"))
	require.Contains(t, created, "2026-01-01T07:00:00Z")
	res, err := e.svc.Extract(context.Background(), 2, "https://s.test/p")
	require.NoError(t, err)
	require.True(t, res.Cached)
}

func TestRateLimitSlidingWindowSkipsCacheHits(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for i := range 30 {
		_, err := e.svc.Extract(ctx, 1, "https://s.test/p"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		require.NoError(t, err, "extraction %d", i+1)
		*e.clock = e.clock.Add(time.Minute) // 30 within 30 minutes
	}
	_, err := e.svc.Extract(ctx, 1, "https://s.test/new")
	require.ErrorIs(t, err, ErrRateLimited)

	// A cache hit doesn't count and isn't refused.
	res, err := e.svc.Extract(ctx, 1, "https://s.test/pa"+"a")
	require.NoError(t, err)
	require.True(t, res.Cached)

	// Other members have their own window.
	_, err = e.svc.Extract(ctx, 2, "https://s.test/new")
	require.NoError(t, err)

	// The first extraction ran at t0; it leaves the window at t0+60m, one at a time.
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	*e.clock = start.Add(60*time.Minute - time.Second)
	_, err = e.svc.Extract(ctx, 1, "https://s.test/new2")
	require.ErrorIs(t, err, ErrRateLimited)
	*e.clock = start.Add(60 * time.Minute)
	_, err = e.svc.Extract(ctx, 1, "https://s.test/new2")
	require.NoError(t, err)
	_, err = e.svc.Extract(ctx, 1, "https://s.test/new3")
	require.ErrorIs(t, err, ErrRateLimited, "only one slot freed")
}

func TestInvalidURL(t *testing.T) {
	e := newEnv(t, nil)
	_, err := e.svc.Extract(context.Background(), 1, "ftp://nope")
	require.ErrorIs(t, err, ErrInvalidURL)
	require.Zero(t, e.direct.calls)
}

func TestMetricsAreExposedWithNames(t *testing.T) {
	e := newEnv(t, nil)
	_, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	n, err := testutil.GatherAndCount(e.reg, "wishbone_extractions_total")
	require.NoError(t, err)
	require.Positive(t, n)
}

// A real Direct against a server that blocks the Chrome user agent: the retry's
// page reaches the LLM when Bright Data fails.
func TestPreviewRetryPageReachesLLMWhenBrightDataFails(t *testing.T) {
	stub, err := os.ReadFile(filepath.Join("..", "testdata", "captcha_stub.html"))
	require.NoError(t, err)
	product, err := os.ReadFile(filepath.Join("..", "testdata", "product_no_metadata.html"))
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.UserAgent() == extract.DefaultUserAgent {
			_, _ = w.Write(stub)
			return
		}
		_, _ = w.Write(product)
	}))
	defer srv.Close()

	e := newEnv(t, func(o *Options) {
		o.Direct = extract.NewDirect(extract.Options{AllowPrivateAddrs: true})
	})
	e.bd.err = errors.New("401")
	e.filler.fill = func(p extract.Product) extract.Product { p.Title = "Acme Trail Lantern"; p.Price = "24.99"; return p }

	res, err := e.svc.Extract(context.Background(), 1, srv.URL+"/p")
	require.NoError(t, err)
	require.Equal(t, StageLLM, res.Stage)
	require.Equal(t, "24.99", res.Price)
	require.Equal(t, string(product), string(e.filler.page))
	require.Equal(t, 1.0, stageCount(t, e, "direct", "failure"))
	require.Equal(t, 1.0, stageCount(t, e, "brightdata", "failure"))
	require.Equal(t, 1.0, stageCount(t, e, "llm", "success"))
}

func TestOptionsSurviveTheLLMStageAndTheCache(t *testing.T) {
	e := newEnv(t, nil)
	opts := []extract.Option{{Name: "Size", Values: []string{"S", "M"}, Selected: "M"}, {Name: "Color"}}
	e.direct.res = extract.Result{Status: extract.StatusOK, URL: "https://s.test/p", Body: []byte("<html/>"),
		Product: extract.Product{Title: "Widget", Options: opts}}
	e.filler.fill = func(p extract.Product) extract.Product { p.Price, p.Currency = "9.00", "USD"; return p }
	first, err := e.svc.Extract(context.Background(), 1, "https://s.test/p")
	require.NoError(t, err)
	require.Equal(t, opts, first.Options, "the LLM stage doesn't touch options")
	require.Equal(t, 1, e.filler.calls)

	second, err := e.svc.Extract(context.Background(), 2, "https://s.test/p")
	require.NoError(t, err)
	require.True(t, second.Cached)
	require.Equal(t, opts, second.Options)
}

func TestACachedResultFromBeforeOptionsHasNone(t *testing.T) {
	e := newEnv(t, nil)
	_, err := e.options.DB.Exec(`INSERT INTO extraction_cache (url, result, created_at) VALUES (?, ?, ?)`,
		"https://s.test/old", `{"Title":"Old","Images":null,"Price":"1.00","Currency":"USD","URL":"https://s.test/old","Store":"s.test","Stage":"direct"}`, *e.clock)
	require.NoError(t, err)
	res, err := e.svc.Extract(context.Background(), 1, "https://s.test/old")
	require.NoError(t, err)
	require.True(t, res.Cached)
	require.Empty(t, res.Options)
}

type fakePhoto struct {
	guess llm.PhotoGuess
	usage llm.Usage
	err   error
	calls int
	got   []byte
}

func (f *fakePhoto) GuessPhoto(_ context.Context, jpeg []byte) (llm.PhotoGuess, llm.Usage, error) {
	f.calls++
	f.got = jpeg
	return f.guess, f.usage, f.err
}

func TestGuessPhoto(t *testing.T) {
	ph := &fakePhoto{guess: llm.PhotoGuess{Title: "Kettle", Price: "20.00", Currency: "USD"}, usage: llm.Usage{InputTokens: 11, OutputTokens: 4}}
	e := newEnv(t, func(o *Options) { o.Photo = ph })
	got, err := e.svc.GuessPhoto(context.Background(), 1, []byte("jpeg"))
	require.NoError(t, err)
	require.Equal(t, ph.guess, got)
	require.Equal(t, []byte("jpeg"), ph.got)
	require.Equal(t, 11.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "input"}))
	require.Equal(t, 4.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "output"}))
	require.Equal(t, 1.0, stageCount(t, e, StageLLM, "success"))
}

func TestGuessPhotoNotConfiguredCostsNothing(t *testing.T) {
	e := newEnv(t, nil)
	for range MemberLimit + 1 {
		got, err := e.svc.GuessPhoto(context.Background(), 1, []byte("jpeg"))
		require.NoError(t, err)
		require.Equal(t, llm.PhotoGuess{}, got)
	}
}

func TestGuessPhotoFailureCountsAndIsReported(t *testing.T) {
	ph := &fakePhoto{err: errors.New("boom"), usage: llm.Usage{InputTokens: 5}}
	e := newEnv(t, func(o *Options) { o.Photo = ph })
	_, err := e.svc.GuessPhoto(context.Background(), 1, []byte("jpeg"))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRateLimited)
	require.Equal(t, 1.0, stageCount(t, e, StageLLM, "failure"))
	require.Equal(t, 5.0, counter(t, e, "wishbone_llm_tokens_total", map[string]string{"direction": "input"}))
}

func TestGuessPhotoSharesTheMemberLimit(t *testing.T) {
	ph := &fakePhoto{}
	e := newEnv(t, func(o *Options) { o.Photo = ph })
	for range MemberLimit {
		_, err := e.svc.GuessPhoto(context.Background(), 1, []byte("jpeg"))
		require.NoError(t, err)
	}
	_, err := e.svc.GuessPhoto(context.Background(), 1, []byte("jpeg"))
	require.ErrorIs(t, err, ErrRateLimited)
	require.Equal(t, MemberLimit, ph.calls, "the call over the limit makes no LLM request")

	_, err = e.svc.Extract(context.Background(), 1, "https://s.test/other")
	require.ErrorIs(t, err, ErrRateLimited, "extractions share the same limit")

	_, err = e.svc.GuessPhoto(context.Background(), 2, []byte("jpeg"))
	require.NoError(t, err, "another member has their own limit")
}

func TestPhotoLimitReachedCountsNothing(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Photo = &fakePhoto{} })
	for range MemberLimit + 3 {
		require.False(t, e.svc.PhotoLimitReached(1), "checking uses up no attempt")
	}
	for range MemberLimit {
		_, err := e.svc.GuessPhoto(context.Background(), 1, []byte("jpeg"))
		require.NoError(t, err)
	}
	require.True(t, e.svc.PhotoLimitReached(1))
	require.False(t, e.svc.PhotoLimitReached(2))
}

func TestPhotoLimitReachedIsFalseWithoutALLM(t *testing.T) {
	e := newEnv(t, nil)
	for range MemberLimit + 1 {
		require.False(t, e.svc.PhotoLimitReached(1))
	}
}
