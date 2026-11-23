package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEstimateModelCostMicroUSD(t *testing.T) {
	t.Parallel()
	lookup := func(_, model string) (ModelPricing, bool) {
		if model == "gpt-5" {
			return ModelPricing{Input: 2500000, Cached: 250000, Output: 10000000, Currency: "USD"}, true
		}
		if model == "eur-model" {
			return ModelPricing{Input: 100, Output: 200, Currency: "EUR"}, true
		}
		return ModelPricing{}, false
	}

	if cost, known := EstimateModelCostMicroUSD("local", "gpt-5", 1000, 0, 1000, lookup); known || cost != 0 {
		t.Fatalf("local provider should not fabricate cost: cost=%d known=%v", cost, known)
	}
	if _, known := EstimateModelCostMicroUSD("openai", "eur-model", 1000, 0, 1000, lookup); known {
		t.Fatal("non-USD currency should return known=false")
	}
	// Cached clamped to input; ceiling division to nearest micro-USD.
	cost, known := EstimateModelCostMicroUSD("openai", "gpt-5", 100, 500, 10, lookup)
	want := int64((100*250000 + 10*10000000 + 999999) / 1000000)
	if !known || cost != want {
		t.Fatalf("got cost=%d known=%v, want cost=%d", cost, known, want)
	}
}

func TestFetchJSONAndSearchParsers(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			_, _ = w.Write([]byte(strings.Repeat("a", 50)))
			return
		}
		if r.URL.Path == "/err" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if b, err := FetchJSON(context.Background(), srv.Client(), srv.URL+"/ok", 64); err != nil || string(b) != `{"ok":true}` {
		t.Fatalf("FetchJSON ok failed: %q err=%v", b, err)
	}
	if _, err := FetchJSON(context.Background(), srv.Client(), srv.URL+"/big", 16); err == nil {
		t.Fatal("expected size limit failure")
	}
	if _, err := FetchJSON(context.Background(), srv.Client(), srv.URL+"/err", 64); err == nil {
		t.Fatal("expected HTTP error failure")
	}

	type hit struct{ title, url, snippet, source string }
	var hits []hit
	err := ParseDuckDuckGo([]byte(`{
		"Heading":"Solana",
		"AbstractURL":"https://solana.com",
		"AbstractText":"Fast L1",
		"RelatedTopics":[
			{"Topics":[{"FirstURL":"https://solana.com/docs","Text":"Solana Docs - Developer Portal"}]}
		]
	}`), 5, func(t, u, s, src string) { hits = append(hits, hit{t, u, s, src}) })
	if err != nil || len(hits) != 2 || hits[1].title != "Solana Docs" {
		t.Fatalf("ParseDuckDuckGo unexpected hits=%+v err=%v", hits, err)
	}

	var wikiHits []hit
	err = ParseWikipedia([]byte(`{"query":{"search":[{"title":"Proof of History","snippet":"<span>consensus</span>"}]}}`), 5,
		func(raw string) string { return strings.ReplaceAll(raw, "<span>", "") },
		func(t, u, s, src string) { wikiHits = append(wikiHits, hit{t, u, s, src}) },
	)
	if err != nil || len(wikiHits) != 1 || wikiHits[0].url != "https://en.wikipedia.org/wiki/Proof_of_History" {
		t.Fatalf("ParseWikipedia unexpected hits=%+v err=%v", wikiHits, err)
	}
}
