package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestGenerateUsesStatelessStructuredRequestWithoutPromptCacheKey(t *testing.T) {
	var requestBody map[string]any
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &requestBody); err != nil {
			t.Fatal(err)
		}
		body := `{
            "id":"resp_1","object":"response","status":"completed",
            "output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"insert_text\":\"value\"}","annotations":[]}]}],
            "usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":3},"output_tokens":4}
        }`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	client := openai.NewClient(
		option.WithBaseURL("http://generate.test"),
		option.WithAPIKey("test"),
		option.WithHTTPClient(httpClient),
	)
	cfg := &Config{client: &client}

	result, err := cfg.Generate(context.Background(), GenerateOptions{
		Model:        "gpt-5.6-luna",
		Effort:       "none",
		Instructions: "complete code",
		Input:        "const value = ",
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"insert_text": map[string]any{"type": "string"},
			},
		},
		MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != `{"insert_text":"value"}` {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Usage.InputTokens != 12 || result.Usage.CachedTokens != 3 || result.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if requestBody["store"] != false || requestBody["max_output_tokens"] != float64(256) {
		t.Fatalf("stateless limits missing from request: %#v", requestBody)
	}
	if _, ok := requestBody["prompt_cache_key"]; ok {
		t.Fatalf("request contains prompt_cache_key: %#v", requestBody)
	}
	if _, ok := requestBody["tools"]; ok {
		t.Fatalf("request contains tools: %#v", requestBody["tools"])
	}
	text, ok := requestBody["text"].(map[string]any)
	if !ok {
		t.Fatalf("text config = %#v", requestBody["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok || format["type"] != "json_schema" || format["strict"] != true {
		t.Fatalf("format = %#v", text["format"])
	}
	reasoning, ok := requestBody["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", requestBody["reasoning"])
	}
}
