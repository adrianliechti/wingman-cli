package proxy

import (
	"encoding/json"
	"strings"
)

// Gemini: /v1beta/models/{model}:generateContent, /v1beta/models/{model}:streamGenerateContent

func metadataFromGemini(respBody []byte, path string) metadata {
	var m metadata

	m.Model = extractGeminiModel(path)

	if len(respBody) == 0 {
		return m
	}

	var obj struct {
		ModelVersion  string `json:"modelVersion"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}

	if json.Unmarshal(respBody, &obj) == nil {
		if obj.ModelVersion != "" {
			m.Model = obj.ModelVersion
		}

		m.InputTokens = obj.UsageMetadata.PromptTokenCount
		m.OutputTokens = obj.UsageMetadata.CandidatesTokenCount
	}

	return m
}

func metadataFromGeminiSSE(sseBody []byte, path string) metadata {
	var m metadata

	m.Model = extractGeminiModel(path)

	// last SSE chunk has cumulative usageMetadata
	for _, data := range sseData(sseBody) {
		var obj struct {
			ModelVersion  string `json:"modelVersion"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}

		if json.Unmarshal([]byte(data), &obj) != nil {
			continue
		}

		in := obj.UsageMetadata.PromptTokenCount
		out := obj.UsageMetadata.CandidatesTokenCount

		if in > 0 || out > 0 {
			if obj.ModelVersion != "" {
				m.Model = obj.ModelVersion
			}

			m.InputTokens = in
			m.OutputTokens = out
		}
	}

	return m
}

// extractGeminiModel extracts the model name from a Gemini API path.
// e.g. /v1beta/models/gemini-pro:generateContent -> gemini-pro
func extractGeminiModel(path string) string {
	for _, prefix := range []string{"/v1/models/", "/v1beta/models/"} {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}

		if model, _, ok := strings.Cut(rest, ":"); ok {
			return model
		}

		return rest
	}

	return ""
}
