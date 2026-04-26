package proxy

import "encoding/json"

// Anthropic: /v1/messages

func metadataFromAnthropic(reqBody, respBody []byte) metadata {
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
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if json.Unmarshal(respBody, &obj) == nil {
		m.InputTokens = obj.Usage.InputTokens
		m.OutputTokens = obj.Usage.OutputTokens
	}

	return m
}

func metadataFromAnthropicSSE(reqBody, sseBody []byte) metadata {
	var m metadata

	m.Model = extractJSONField(reqBody, "model")

	// message_start contains model + input tokens
	// message_delta contains output tokens
	for _, data := range sseData(sseBody) {
		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}

		if json.Unmarshal([]byte(data), &msg) != nil {
			continue
		}

		switch msg.Type {
		case "message_start":
			if msg.Message.Model != "" {
				m.Model = msg.Message.Model
			}
			m.InputTokens = msg.Message.Usage.InputTokens

		case "message_delta":
			m.OutputTokens = msg.Usage.OutputTokens
		}
	}

	return m
}
