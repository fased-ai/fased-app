package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// WebSearchResult describes one sanitized untrusted search hit returned by web_search.
type WebSearchResult struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Domain  string `json:"domain"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"`
}

// FetchJSON fetches an HTTP 2xx JSON payload up to maxBytes using client.
func FetchJSON(ctx context.Context, client *http.Client, targetURL string, maxBytes int64) ([]byte, error) {
	if client == nil || maxBytes <= 0 {
		return nil, errors.New("search client unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "fased-agent/1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search provider returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("search provider response exceeds size limit")
	}
	return body, nil
}

// ParseDuckDuckGo decodes DuckDuckGo Instant Answer JSON and invokes add for abstract and nested topic hits.
func ParseDuckDuckGo(body []byte, _ int, add func(title, rawURL, snippet, source string)) error {
	type ddgTopic struct {
		FirstURL string     `json:"FirstURL"`
		Text     string     `json:"Text"`
		Topics   []ddgTopic `json:"Topics"`
	}
	var ddg struct {
		Heading       string     `json:"Heading"`
		AbstractText  string     `json:"AbstractText"`
		AbstractURL   string     `json:"AbstractURL"`
		RelatedTopics []ddgTopic `json:"RelatedTopics"`
	}
	if err := json.Unmarshal(body, &ddg); err != nil {
		return err
	}
	if ddg.AbstractURL != "" && add != nil {
		add(ddg.Heading, ddg.AbstractURL, ddg.AbstractText, "duckduckgo")
	}
	var walkTopics func(items []ddgTopic)
	walkTopics = func(items []ddgTopic) {
		for _, item := range items {
			if item.FirstURL != "" && add != nil {
				title := item.Text
				if cut := strings.Index(title, " - "); cut > 0 {
					title = title[:cut]
				}
				add(title, item.FirstURL, item.Text, "duckduckgo")
			}
			if len(item.Topics) > 0 {
				walkTopics(item.Topics)
			}
		}
	}
	walkTopics(ddg.RelatedTopics)
	return nil
}

// ParseWikipedia decodes Wikipedia Action API search JSON and invokes add with escaped article URLs.
func ParseWikipedia(body []byte, _ int, cleanHTML func(string) string, add func(title, rawURL, snippet, source string)) error {
	var wiki struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &wiki); err != nil {
		return err
	}
	for _, hit := range wiki.Query.Search {
		pageURL := "https://en.wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(strings.TrimSpace(hit.Title), " ", "_"))
		snippet := hit.Snippet
		if cleanHTML != nil {
			snippet = cleanHTML(hit.Snippet)
		}
		if add != nil {
			add(hit.Title, pageURL, snippet, "wikipedia")
		}
	}
	return nil
}
