package search

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func SearchTool() tool.Tool {
	return tool.Tool{
		Name:        "search_online",
		Description: "Search online if the requested information cannot be found in the language model or the information could be present in a time after the language model was trained",

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "the text to search online for",
				},
			},

			"required": []string{"query"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			query, ok := args["query"].(string)

			if !ok {
				return "", errors.New("missing query parameter")
			}

			results, err := search(ctx, query)

			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No results found.", nil
			}

			var sb strings.Builder

			for i, r := range results {
				sb.WriteString(fmt.Sprintf("## %d. %s\n", i+1, r.Title))
				sb.WriteString(fmt.Sprintf("URL: %s\n", r.URL))
				sb.WriteString(fmt.Sprintf("%s\n\n", r.Content))
			}

			return sb.String(), nil
		},
	}
}

func Tools() []tool.Tool {
	return []tool.Tool{
		SearchTool(),
	}
}

type result struct {
	URL     string
	Title   string
	Content string
}

func search(ctx context.Context, query string) ([]result, error) {
	u, _ := url.Parse("https://duckduckgo.com/html/")

	values := u.Query()
	values.Set("q", query)
	u.RawQuery = values.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("Referer", "https://www.duckduckgo.com/")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.4 Safari/605.1.15")

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	var results []result

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
			snippet = normalize(snippet)
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
			snippet = normalize(snippet)
			resultSnippet = snippet
		}

		if resultSnippet == "" {
			continue
		}

		r := result{
			URL:     resultURL,
			Title:   resultTitle,
			Content: resultSnippet,
		}

		results = append(results, r)
		resultURL = ""
		resultTitle = ""
		resultSnippet = ""
	}

	return results, nil
}

// normalize cleans up text by collapsing whitespace and trimming
func normalize(s string) string {
	var sb strings.Builder
	var lastSpace bool

	for _, r := range s {
		if unicode.IsSpace(r) {
			if !lastSpace {
				sb.WriteRune(' ')
				lastSpace = true
			}
		} else {
			sb.WriteRune(r)
			lastSpace = false
		}
	}

	return strings.TrimSpace(sb.String())
}
