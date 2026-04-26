package proxy

import "encoding/json"

// OpenAI: /v1/chat/completions, /v1/responses, /v1/embeddings

func metadataFromOpenAI(reqBody, respBody []byte) metadata {
	var m metadata

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
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}

	if json.Unmarshal(respBody, &obj) == nil {
		m.InputTokens = obj.Usage.PromptTokens + obj.Usage.InputTokens
		m.CachedTokens = obj.Usage.PromptTokensDetails.CachedTokens + obj.Usage.InputTokensDetails.CachedTokens
		m.OutputTokens = obj.Usage.CompletionTokens + obj.Usage.OutputTokens
	}

	return m
}

func metadataFromOpenAISSE(reqBody, sseBody []byte) metadata {
	var m metadata

	m.Model = extractJSONField(reqBody, "model")

	for _, data := range sseData(sseBody) {
		var obj struct {
			// /v1/chat/completions streaming (final chunk with include_usage)
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				InputTokens      int `json:"input_tokens"`
				OutputTokens     int `json:"output_tokens"`
				PromptTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				InputTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
			// /v1/responses streaming (response.completed / response.done events)
			Response struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					InputTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}

		if json.Unmarshal([]byte(data), &obj) != nil {
			continue
		}

		in := obj.Usage.PromptTokens + obj.Usage.InputTokens + obj.Response.Usage.InputTokens
		cached := obj.Usage.PromptTokensDetails.CachedTokens + obj.Usage.InputTokensDetails.CachedTokens + obj.Response.Usage.InputTokensDetails.CachedTokens
		out := obj.Usage.CompletionTokens + obj.Usage.OutputTokens + obj.Response.Usage.OutputTokens

		if in > 0 || out > 0 {
			m.InputTokens = in
			m.CachedTokens = cached
			m.OutputTokens = out
		}
	}

	return m
}
