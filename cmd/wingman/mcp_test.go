package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

func TestParseMCPAddRemote(t *testing.T) {
	name, server, opts, err := parseMCPAdd([]string{"remote", "--url", "https://mcp.example.com/mcp", "-H", "X-Key: 1", "--header", "X-Other:two", "--client-id", "cid", "--callback-port", "4000", "-s", "project", "--no-login"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "remote" || server.URL != "https://mcp.example.com/mcp" || server.Transport != "" {
		t.Fatalf("name = %q server = %+v", name, server)
	}
	if server.Headers["X-Key"] != "1" || server.Headers["X-Other"] != "two" {
		t.Fatalf("headers = %+v", server.Headers)
	}
	if server.OAuth == nil || server.OAuth.ClientID != "cid" || server.OAuth.CallbackPort != 4000 {
		t.Fatalf("oauth = %+v", server.OAuth)
	}
	if opts.Scope != mcpScopeProject || !opts.NoLogin {
		t.Fatalf("opts = %+v", opts)
	}

	_, server, _, err = parseMCPAdd([]string{"events", "--url", "https://x.example/sse", "--transport", "sse"})
	if err != nil || server.Transport != "sse" {
		t.Fatalf("server = %+v err = %v", server, err)
	}
}

func TestParseMCPAddStdio(t *testing.T) {
	name, server, opts, err := parseMCPAdd([]string{"fs", "-e", "A=1", "--env", "B=x=y", "--", "npx", "-y", "server", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "fs" || server.Command != "npx" || strings.Join(server.Args, " ") != "-y server --flag" {
		t.Fatalf("name = %q server = %+v", name, server)
	}
	if server.Env["A"] != "1" || server.Env["B"] != "x=y" || server.URL != "" || server.OAuth != nil {
		t.Fatalf("server = %+v", server)
	}
	if opts.Scope != mcpScopeGlobal {
		t.Fatalf("scope = %q", opts.Scope)
	}
}

func TestParseMCPAddErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"name"},
		{"name", "--url", "https://x", "--", "cmd"},
		{"name", "--url", "ftp://x"},
		{"name", "--url", "https://x", "--transport", "grpc"},
		{"name", "--url", "https://x", "-H", "nocolon"},
		{"name", "--url", "https://x", "-e", "A=1"},
		{"name", "-H", "X: 1", "--", "cmd"},
		{"name", "-e", "novalue", "--", "cmd"},
		{"name", "--scope", "elsewhere", "--", "cmd"},
		{"bad name", "--", "cmd"},
	}

	for _, args := range cases {
		if _, _, _, err := parseMCPAdd(args); err == nil {
			t.Errorf("%q: expected an error", args)
		}
	}
}

func TestMCPAddListRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WINGMAN_HOME", home)

	project := t.TempDir()
	t.Chdir(project)

	if err := runMCPAdd(t.Context(), []string{"fs", "--", "npx", "-y", "server"}); err != nil {
		t.Fatal(err)
	}
	if err := runMCPAdd(t.Context(), []string{"remote", "--url", "https://remote.example/mcp", "--scope", "project", "--no-login"}); err != nil {
		t.Fatal(err)
	}
	if err := runMCPAdd(t.Context(), []string{"fs", "--url", "https://shadow.example/mcp", "--scope", "project", "--no-login"}); err != nil {
		t.Fatal(err)
	}

	global, err := mcp.LoadConfig(filepath.Join(home, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if global.Servers["fs"].Command != "npx" {
		t.Fatalf("global = %+v", global.Servers)
	}

	local, err := mcp.LoadConfig(filepath.Join(project, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if local.Servers["remote"].URL != "https://remote.example/mcp" || local.Servers["fs"].URL != "https://shadow.example/mcp" {
		t.Fatalf("project = %+v", local.Servers)
	}

	entries, err := loadMCPEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "fs" || entries[0].Scope != mcpScopeProject || entries[1].Name != "remote" {
		t.Fatalf("entries = %+v", entries)
	}

	var out bytes.Buffer
	printMCPEntry(&out, entries[1], mcp.NewCredentialStore(filepath.Join(home, "none.json")))
	if !strings.Contains(out.String(), "URL:       https://remote.example/mcp") || !strings.Contains(out.String(), "not logged in") {
		t.Fatalf("output = %s", out.String())
	}

	if err := runMCPRemove([]string{"fs"}); err != nil {
		t.Fatal(err)
	}
	if err := runMCPRemove([]string{"fs"}); err == nil {
		t.Fatal("expected an error removing a missing server")
	}

	entries, err = loadMCPEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "remote" {
		t.Fatalf("entries = %+v", entries)
	}

	if _, err := os.Stat(filepath.Join(home, "mcp.json")); err != nil {
		t.Fatal(err)
	}
}
