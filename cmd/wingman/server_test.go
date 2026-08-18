package main

import "testing"

func TestParseServerCommandDefaults(t *testing.T) {
	opts, err := parseServerCommand(nil)
	if err != nil {
		t.Fatal(err)
	}

	if opts.Host != "localhost" || opts.WorkDir != "" || opts.Port != 0 || opts.PreviewPort != 0 || opts.NoBrowser {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
}

func TestParseServerCommandRemoteOptions(t *testing.T) {
	opts, err := parseServerCommand([]string{
		"--host", "0.0.0.0",
		"--port=9000",
		"--preview-port", "9001",
		"-C", "/workspace",
		"--no-browser",
	})
	if err != nil {
		t.Fatal(err)
	}

	if opts.Host != "0.0.0.0" || opts.WorkDir != "/workspace" || opts.Port != 9000 || opts.PreviewPort != 9001 || !opts.NoBrowser {
		t.Fatalf("unexpected options: %+v", opts)
	}
}
