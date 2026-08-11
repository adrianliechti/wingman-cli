package external

import (
	"context"
	"strings"
	"testing"
)

func TestWithDefaultsDoesNotAssumeLocalServer(t *testing.T) {
	t.Setenv("WINGMAN_URL", "")
	t.Setenv("WINGMAN_TOKEN", "")

	options := WithDefaults(nil)

	if options.WingmanURL != "" {
		t.Fatalf("WingmanURL = %q, want empty", options.WingmanURL)
	}
	if options.WingmanToken != "-" {
		t.Fatalf("WingmanToken = %q, want placeholder token", options.WingmanToken)
	}
}

func TestAvailableModelsRequiresWingmanURL(t *testing.T) {
	t.Setenv("WINGMAN_URL", "")

	_, err := AvailableModels(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "WINGMAN_URL is required") {
		t.Fatalf("AvailableModels() error = %v, want missing WINGMAN_URL error", err)
	}
}
