package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const maxFetchBytes = 100 * 1024 // 100KB max content

func FetchTool() tool.Tool {
	description := strings.Join([]string{
		"Fetch content from a URL and return it as text. HTML pages are converted to readable text.",
		"",
		"Usage:",
		"- Use this to read documentation, API references, or any web content.",
		"- The URL must be a fully-formed valid URL (e.g., https://example.com).",
		"- Returns extracted text content from the page.",
		"- Large responses are truncated to 100KB.",
	}, "\n")

	return tool.Tool{
		Name:        "fetch",
		Description: description,

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch content from",
				},
			},

			"required": []string{"url"},
		},

		Execute: func(ctx context.Context, env *tool.Environment, args map[string]any) (string, error) {
			urlStr, ok := args["url"].(string)

			if !ok || urlStr == "" {
				return "", fmt.Errorf("url is required")
			}

			wingmanURL := os.Getenv("WINGMAN_URL")

			if wingmanURL == "" {
				return "", fmt.Errorf("fetch is not available: WINGMAN_URL is not configured")
			}

			return extractWingman(ctx, wingmanURL, os.Getenv("WINGMAN_TOKEN"), urlStr)
		},
	}
}

func Tools() []tool.Tool {
	if os.Getenv("WINGMAN_URL") == "" {
		return nil
	}

	return []tool.Tool{
		FetchTool(),
	}
}

func extractWingman(ctx context.Context, baseURL, token, urlStr string) (string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/extract"

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("url", urlStr); err != nil {
		return "", err
	}

	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &body)

	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("extract API returned HTTP %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxFetchBytes+1)

	data, err := io.ReadAll(limited)

	if err != nil {
		return "", err
	}

	content := strings.TrimSpace(string(data))

	if len(data) > maxFetchBytes {
		content = content[:maxFetchBytes] + "\n\n[Content truncated at 100KB]"
	}

	if content == "" {
		return "", fmt.Errorf("empty response from extract API")
	}

	return content, nil
}
