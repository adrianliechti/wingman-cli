package shell

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestDefaultEnvironmentPolicyScrubsAgentCredentials(t *testing.T) {
	policy := DefaultEnvironmentPolicy()
	got := policy.Filter([]string{
		"PATH=/bin",
		"OPENAI_API_KEY=openai-secret",
		"ANTHROPIC_AUTH_TOKEN=anthropic-secret",
		"ANTHROPIC_BASE_URL=https://example.test",
		"GITHUB_TOKEN=workflow-token",
	})
	joined := strings.Join(got, "\n")
	for _, secret := range []string{"openai-secret", "anthropic-secret", "ANTHROPIC_BASE_URL"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("filtered environment leaked %q: %s", secret, joined)
		}
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "GITHUB_TOKEN=workflow-token") {
		t.Fatalf("ordinary development environment was removed: %s", joined)
	}
}

func TestEnvironmentPolicyAllowAndReplaceOverrideDeny(t *testing.T) {
	policy := EnvironmentPolicy{
		Deny:    []string{"SECRET_*"},
		Allow:   []string{"SECRET_NEEDED"},
		Replace: map[string]string{"SECRET_REPLACED": "session-value"},
	}
	got := strings.Join(policy.Filter([]string{
		"SECRET_DROP=host-value",
		"SECRET_NEEDED=allowed-value",
		"SECRET_REPLACED=host-value",
	}), "\n")
	if strings.Contains(got, "SECRET_DROP") || strings.Contains(got, "host-value") {
		t.Fatalf("denied values survived: %s", got)
	}
	if !strings.Contains(got, "SECRET_NEEDED=allowed-value") || !strings.Contains(got, "SECRET_REPLACED=session-value") {
		t.Fatalf("explicit overrides missing: %s", got)
	}
}

func TestToolCommandUsesFilteredEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	t.Setenv("WINGMAN_TEST_VISIBLE", "visible")

	cmd := buildToolCommand(context.Background(), environmentPrintCommand(), t.TempDir(), nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, "must-not-leak") || !strings.Contains(text, "visible") {
		t.Fatalf("unexpected tool environment: %q", text)
	}
}

func TestCustomEnvironmentPolicyExtendsDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "default-denied")
	t.Setenv("WINGMAN_TEST_SECRET", "custom-denied")
	t.Setenv("WINGMAN_TEST_VISIBLE", "visible")

	environ := strings.Join(environmentForTools(&Options{Environment: &EnvironmentPolicy{
		Deny: []string{"WINGMAN_TEST_SECRET"},
	}}), "\n")
	if strings.Contains(environ, "default-denied") || strings.Contains(environ, "custom-denied") {
		t.Fatalf("custom policy disabled a deny rule: %s", environ)
	}
	if !strings.Contains(environ, "WINGMAN_TEST_VISIBLE=visible") {
		t.Fatalf("custom policy removed an ordinary variable: %s", environ)
	}
}

func environmentPrintCommand() string {
	if os.PathSeparator == '\\' {
		return `[Console]::WriteLine("$env:OPENAI_API_KEY|$env:WINGMAN_TEST_VISIBLE")`
	}
	return `printf '%s|%s' "$OPENAI_API_KEY" "$WINGMAN_TEST_VISIBLE"`
}
