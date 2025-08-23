package duckduckgo

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/text"
)

func New() (*Client, error) {
	c := &Client{
		client: http.DefaultClient,
	}

	return c, nil
}

var (
	_ tool.Provider = (*Client)(nil)
)

type Client struct {
	client *http.Client
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	query_tool := tool.Tool{
		Name:        "search_online",
		Description: "Search online if the requested information cannot be found in the language model or the information could be present in a time after the language model was trained",

		Schema: &tool.Schema{
			Type: "object",

			Properties: map[string]*tool.Schema{
				"query": {
					Type:        "string",
					Description: "the text to search online for",
				},
			},

			Required: []string{"query"},
		},

		ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
			query, ok := params["query"].(string)

			if !ok {
				return nil, errors.New("missing query parameter")
			}

			results, err := c.Query(ctx, query)

			if err != nil {
				return nil, err
			}

			return results, nil
		},
	}

	return []tool.Tool{
		query_tool,
	}, nil
}

func (c *Client) Query(ctx context.Context, query string) ([]Result, error) {
	url, _ := url.Parse("https://duckduckgo.com/html/")

	values := url.Query()
	values.Set("q", query)

	url.RawQuery = values.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", url.String(), nil)
	req.Header.Set("Referer", "https://www.duckduckgo.com/")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15")

	resp, err := c.client.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	var results []Result

	regexLink := regexp.MustCompile(`href="([^"]+)"`)
	regexSnippet := regexp.MustCompile(`<[^>]*>`)

	scanner := bufio.NewScanner(resp.Body)

	var resultURL string
	var resultTitle string
	var resultSnippet string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "result__a") {
			snippet := regexSnippet.ReplaceAllString(line, "")
			snippet = text.Normalize(snippet)
			resultTitle = snippet
		}

		if strings.Contains(line, "result__url") {
			links := regexLink.FindStringSubmatch(line)
			if len(links) >= 2 {
				resultURL = links[1]
			}
		}

		if strings.Contains(line, "result__snippet") {
			snippet := regexSnippet.ReplaceAllString(line, "")
			snippet = text.Normalize(snippet)
			resultSnippet = snippet
		}

		if resultSnippet == "" {
			continue
		}

		result := Result{
			URL: resultURL,

			Title:   resultTitle,
			Content: resultSnippet,
		}

		results = append(results, result)
		resultURL = ""
		resultTitle = ""
		resultSnippet = ""
	}

	return results, nil
}

type Result struct {
	URL string

	Title   string
	Content string
}
