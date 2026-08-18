package dap

// knownAdapters is deliberately data-only. Adapter-specific launch and attach
// arguments are supplied by the AI and interpreted by the adapter itself.
// Supporting another language starts by adding a descriptor here; the manager,
// protocol client, tool, and editor bridge remain unchanged.
var knownAdapters = []Adapter{
	{
		Name:             "delve",
		Language:         "Go",
		AdapterID:        "go",
		Command:          "dlv",
		Args:             []string{"dap", "--listen=127.0.0.1:0"},
		Transport:        TransportTCP,
		ReadyPrefix:      "DAP server listening at:",
		TerminalStrategy: TerminalAdapterProcess,
		Markers:          []string{"go.mod", "go.work"},
		Defaults: map[string]any{
			"type": "go",
		},
		ConfigurationPaths: []ConfigurationPath{
			{Key: "program"},
			{Key: "cwd", Directory: true},
			{Key: "dlvCwd", Directory: true},
			{Key: "coreFilePath"},
			{Key: "traceDirPath", Directory: true},
		},
		ConfigurationHint: "launch: configuration requires mode (debug, test, exec, replay, or core) and program; attach: mode local with processId. program and other path fields are project-relative paths (for example ./cmd/server, not an import path). For a detected main or test, program is normally its directory; a single test uses mode test with an anchored -test.run argument. Delve also accepts args, cwd, env, buildFlags, stopOnEntry, and noDebug. Use integratedTerminal when the debuggee requires interactive stdin or a full-screen TUI; otherwise use internalConsole.",
	},
}
