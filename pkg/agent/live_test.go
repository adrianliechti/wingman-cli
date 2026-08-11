package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// TestLiveOpenAIStreaming runs the full streaming loop against the real
// OpenAI API: reasoning with summaries, a tool round that replays the
// reasoning item mid-turn, and a second turn that replays the whole
// transcript. It only runs against OpenAI directly — with WINGMAN_URL set it
// skips, so run it via:
//
//	env -u WINGMAN_URL -u WINGMAN_TOKEN go test ./pkg/agent -run TestLiveOpenAI -v
func TestLiveOpenAIStreaming(t *testing.T) {
	if _, ok := os.LookupEnv("WINGMAN_URL"); ok {
		t.Skip("WINGMAN_URL is set; remove it to run against OpenAI directly")
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	model := DefaultModel()
	if model == "" {
		model = "gpt-5.1"
	}

	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}

	cfg.Model = func() string { return model }
	cfg.Effort = func() string { return "low" }
	cfg.Tools = func() []tool.Tool {
		return []tool.Tool{{
			Name:        "get_secret_word",
			Description: "Returns the secret word.",
			Execute: func(context.Context, map[string]any) (tool.Result, error) {
				return tool.Text("pineapple"), nil
			},
		}}
	}

	a := &Agent{Config: cfg}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var text, summary strings.Builder

	run := func(prompt string) string {
		text.Reset()

		stream, err := a.Send(ctx, []Content{{Text: prompt}})
		if err != nil {
			t.Fatal(err)
		}

		for msg, err := range stream {
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}

			for _, c := range msg.Content {
				if c.Text != "" {
					text.WriteString(c.Text)
				}

				if c.Reasoning != nil {
					summary.WriteString(c.Reasoning.Summary)
				}
			}
		}

		return text.String()
	}

	// Turn 1 forces a tool round, so the reasoning item from the first round
	// is replayed within the same turn — the wire shape that previously drew
	// "Missing required parameter: 'input[3].summary'".
	reply := run("Call the get_secret_word tool, then reply with only the word it returns.")

	if !strings.Contains(strings.ToLower(reply), "pineapple") {
		t.Errorf("turn 1 reply = %q, want the tool result echoed", reply)
	}

	// Turn 2 replays the full transcript, including turn 1's reasoning items.
	reply = run("What was the secret word again? Reply with only the word.")

	if !strings.Contains(strings.ToLower(reply), "pineapple") {
		t.Errorf("turn 2 reply = %q, want the word recalled from turn 1", reply)
	}

	// Summaries are model-discretionary: low effort can skip reasoning
	// entirely, so absence is worth noticing but is not a plumbing failure.
	if summary.Len() == 0 {
		t.Log("no reasoning summary deltas streamed this run")
	}

	t.Logf("model %s, summaries: %d bytes", model, summary.Len())
}
