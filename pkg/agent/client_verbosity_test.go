package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestCompleteSendsModelVerbosity(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  any
	}{
		{model: "gpt-6-astra", want: "low"},
		{model: "gpt-5.6-sol", want: nil},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var requestBody map[string]any
			client := streamingTestClient(func(r *http.Request) string {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &requestBody); err != nil {
					t.Fatal(err)
				}
				return "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"resp_1\",\"model\":\"" + tc.model + "\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			})

			if _, err := complete(context.Background(), &client, &request{model: tc.model, effort: "low"}, func(Message, error) bool { return true }); err != nil {
				t.Fatal(err)
			}

			text, _ := requestBody["text"].(map[string]any)
			if got := text["verbosity"]; got != tc.want {
				t.Fatalf("text.verbosity = %#v, want %#v (request %#v)", got, tc.want, requestBody["text"])
			}
		})
	}
}
