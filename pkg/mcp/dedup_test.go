package mcp

import (
	"slices"
	"testing"
)

func TestDedupCollapsesIdenticalRemoteServers(t *testing.T) {
	servers := map[string]ServerConfig{
		"alpha": {URL: "https://x.example/mcp"},
		"beta":  {URL: "https://x.example/mcp"},
	}

	dropped := Dedup(servers, []string{"beta", "alpha"})

	if len(dropped) != 1 {
		t.Fatalf("dropped = %v, want one report", dropped)
	}

	if _, ok := servers["beta"]; !ok {
		t.Fatalf("servers = %v, want the preferred name kept", servers)
	}

	if len(servers) != 1 {
		t.Fatalf("servers = %v, want one entry", servers)
	}
}

func TestDedupKeepsServersDifferingByHeader(t *testing.T) {
	servers := map[string]ServerConfig{
		"alpha": {URL: "https://x.example/mcp", Headers: map[string]string{"X-Tenant": "a"}},
		"beta":  {URL: "https://x.example/mcp", Headers: map[string]string{"X-Tenant": "b"}},
	}

	if dropped := Dedup(servers, nil); len(dropped) != 0 {
		t.Fatalf("dropped = %v, want none", dropped)
	}

	if len(servers) != 2 {
		t.Fatalf("servers = %v, want both entries", servers)
	}
}

func TestDedupDistinguishesTransports(t *testing.T) {
	servers := map[string]ServerConfig{
		"alpha": {URL: "https://x.example/mcp", Transport: "streamable-http"},
		"beta":  {URL: "https://x.example/mcp", Transport: "sse"},
	}

	if dropped := Dedup(servers, nil); len(dropped) != 0 {
		t.Fatalf("dropped = %v, want none", dropped)
	}

	if len(servers) != 2 {
		t.Fatalf("servers = %v, want both entries", servers)
	}
}

func TestDedupCollapsesIdenticalStdioServers(t *testing.T) {
	servers := map[string]ServerConfig{
		"alpha": {Command: "server", Args: []string{"--flag"}, Dir: "/work"},
		"beta":  {Command: "server", Args: []string{"--flag"}, Dir: "/work"},
	}

	if dropped := Dedup(servers, []string{"alpha"}); len(dropped) != 1 {
		t.Fatalf("dropped = %v, want one report", dropped)
	}

	if _, ok := servers["alpha"]; !ok || len(servers) != 1 {
		t.Fatalf("servers = %v, want only alpha", servers)
	}
}

func TestDedupKeepsStdioServersDifferingByEnv(t *testing.T) {
	servers := map[string]ServerConfig{
		"alpha": {Command: "server", Env: map[string]string{"PLUGIN_ROOT": "/a"}},
		"beta":  {Command: "server", Env: map[string]string{"PLUGIN_ROOT": "/b"}},
	}

	if dropped := Dedup(servers, nil); len(dropped) != 0 {
		t.Fatalf("dropped = %v, want none", dropped)
	}

	if len(servers) != 2 {
		t.Fatalf("servers = %v, want both entries", servers)
	}
}

func TestDedupIsDeterministicWithoutPreference(t *testing.T) {
	for range 8 {
		servers := map[string]ServerConfig{
			"alpha": {URL: "https://x.example/mcp"},
			"beta":  {URL: "https://x.example/mcp"},
			"gamma": {URL: "https://x.example/mcp"},
		}

		Dedup(servers, nil)

		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}

		if !slices.Equal(names, []string{"alpha"}) {
			t.Fatalf("kept %v, want alpha every time", names)
		}
	}
}
