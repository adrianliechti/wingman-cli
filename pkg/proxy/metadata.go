package proxy

import (
	"encoding/json"
	"strings"
)

type metadata struct {
	Model string

	InputTokens  int
	CachedTokens int
	OutputTokens int
}

func extractMetadata(path string, reqBody, respBody []byte) metadata {
	streaming := !isJSON(respBody)

	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		if streaming {
			return metadataFromAnthropicSSE(reqBody, respBody)
		}
		return metadataFromAnthropic(reqBody, respBody)

	case isGeminiPath(path):
		if streaming {
			return metadataFromGeminiSSE(respBody, path)
		}
		return metadataFromGemini(respBody, path)

	default:
		if streaming {
			return metadataFromOpenAISSE(reqBody, respBody)
		}
		return metadataFromOpenAI(reqBody, respBody)
	}
}

func isJSON(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}

	return false
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

// sseData returns SSE data payloads in order, skipping [DONE].
func sseData(body []byte) []string {
	lines := strings.Split(string(body), "\n")

	var result []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}

		if data == "[DONE]" {
			continue
		}

		result = append(result, data)
	}

	return result
}
