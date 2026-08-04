package main

import "testing"

func TestParseACPBackend(t *testing.T) {
	tests := []struct {
		value string
		want  acpBackend
	}{
		{value: "native", want: acpBackendNative},
		{value: " NATIVE ", want: acpBackendNative},
		{value: "wingman", want: acpBackendWingman},
		{value: "Wingman", want: acpBackendWingman},
	}

	for _, tt := range tests {
		got, err := parseACPBackend(tt.value)
		if err != nil {
			t.Errorf("parseACPBackend(%q): %v", tt.value, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseACPBackend(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestParseACPBackendRejectsUnknownValue(t *testing.T) {
	if _, err := parseACPBackend("proxy"); err == nil {
		t.Fatal("parseACPBackend(proxy) succeeded")
	}
}
