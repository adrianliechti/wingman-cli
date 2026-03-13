package proxy

import (
	"encoding/json"
	"strings"
)

type Metadata struct {
	Model string

	InputTokens  int
	OutputTokens int
}

func extractMetadata(path string, reqBody, respBody []byte) Metadata {
	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		return metadataFromAnthropic(reqBody, respBody)

	case isGeminiPath(path):
		return metadataFromGemini(reqBody, respBody, path)

	default:
		return metadataFromOpenAI(reqBody, respBody)
	}
}

func extractMetadataSSE(path string, reqBody, sseBody []byte) Metadata {
	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		return metadataFromAnthropicSSE(reqBody, sseBody)

	case isGeminiPath(path):
		return metadataFromGeminiSSE(reqBody, sseBody, path)

	default:
		return metadataFromOpenAISSE(reqBody, sseBody)
	}
}

func isGeminiPath(path string) bool {
	return strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")
}

func extractJSONField(data []byte, field string) string {
	if len(data) == 0 {
		return ""
	}

	var obj map[string]json.RawMessage

	if json.Unmarshal(data, &obj) != nil {
		return ""
	}

	raw, ok := obj[field]
	if !ok {
		return ""
	}

	var val string
	if json.Unmarshal(raw, &val) == nil {
		return val
	}

	return ""
}

// sseDataReverse returns SSE data payloads from last to first, skipping [DONE].
func sseDataReverse(body []byte) []string {
	lines := strings.Split(string(body), "\n")

	var result []string

	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			continue
		}

		result = append(result, data)
	}

	return result
}

// sseDataForward returns SSE data payloads in order.
func sseDataForward(body []byte) []string {
	lines := strings.Split(string(body), "\n")

	var result []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			continue
		}

		result = append(result, data)
	}

	return result
}
