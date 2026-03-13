package proxy

import "encoding/json"

// OpenAI: /v1/chat/completions, /v1/responses, /v1/embeddings

func metadataFromOpenAI(reqBody, respBody []byte) Metadata {
	var m Metadata

	m.Model = extractJSONField(reqBody, "model")

	if len(respBody) == 0 {
		return m
	}

	if model := extractJSONField(respBody, "model"); model != "" {
		m.Model = model
	}

	var obj struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}

	if json.Unmarshal(respBody, &obj) == nil {
		m.InputTokens = obj.Usage.PromptTokens + obj.Usage.InputTokens
		m.OutputTokens = obj.Usage.CompletionTokens + obj.Usage.OutputTokens
	}

	return m
}

func metadataFromOpenAISSE(reqBody, sseBody []byte) Metadata {
	var m Metadata

	m.Model = extractJSONField(reqBody, "model")

	for _, data := range sseData(sseBody) {
		var obj struct {
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				InputTokens      int `json:"input_tokens"`
				OutputTokens     int `json:"output_tokens"`
			} `json:"usage"`
		}

		if json.Unmarshal([]byte(data), &obj) != nil {
			continue
		}

		in := obj.Usage.PromptTokens + obj.Usage.InputTokens
		out := obj.Usage.CompletionTokens + obj.Usage.OutputTokens

		if in > 0 || out > 0 {
			m.InputTokens = in
			m.OutputTokens = out
		}
	}

	return m
}
