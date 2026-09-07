package debugadapter

import "github.com/adrianliechti/wingman-agent/pkg/dap"

func lldbDescriptor(name, language string) dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name: name, Language: language, AdapterID: "lldb", Command: "codelldb",
		Transport: dap.TransportStdio, TerminalStrategy: dap.TerminalRunInTerminal,
		Defaults: map[string]any{"type": "lldb"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program", AllowMissing: true},
			{Key: "cwd", Directory: true},
		},
		IOConfigKey: "terminal",
		IOValues:    map[dap.IOMode]string{dap.IOOutput: "console", dap.IOTerminal: "integrated"},
	}
}
