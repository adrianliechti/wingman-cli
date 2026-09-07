package language_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/devtools"
	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/language"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestLiveManagedTyAndClangd(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_LSP") == "" {
		t.Skip("set WINGMAN_LIVE_LSP=1 to install and run ty and clangd from PyPI")
	}
	t.Setenv("WINGMAN_HOME", t.TempDir())
	t.Setenv("WINGMAN_MANAGED_TOOLS", "on")
	t.Setenv("VIRTUAL_ENV", "")
	if err := os.Unsetenv("VIRTUAL_ENV"); err != nil {
		t.Fatal(err)
	}
	tools, err := devtools.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		server, marker, flags, file, helper, declaration, invalid, valid, symbol string
		column                                                                   int
	}{
		{
			server: "ty", marker: "pyproject.toml", flags: "[project]\nname = 'wingman-lsp-test'\nversion = '0.0.0'\n",
			file: "main.py", declaration: "def greet(name: str) -> str:\n    return name\n",
			invalid: "from installed_dependency import greet\n\nvalue: str = greet(42)\n",
			valid:   "from installed_dependency import greet\n\nvalue: str = greet('world')\n",
			symbol:  "greet", column: 14,
		},
		{
			server: "clangd", marker: "compile_flags.txt", flags: "-std=c++17\n",
			file: "main.cpp", helper: "helper.h", declaration: "inline int twice(int value) { return value * 2; }\n",
			invalid: "#include \"helper.h\"\nint main() {\n    return twice(\"bad\");\n}\n",
			valid:   "#include \"helper.h\"\nint main() {\n    return twice(21);\n}\n",
			symbol:  "twice", column: 12,
		},
		{
			server: "clangd", marker: "compile_flags.txt", flags: "-std=c17\n-Wall\n-Wextra\n",
			file: "main.c", helper: "helper.h", declaration: "#include <stdio.h>\nstatic inline int twice(int value) { return value * 2; }\n",
			invalid: "#include \"helper.h\"\nint main(void) {\n    return twice(\"bad\");\n}\n",
			valid:   "#include \"helper.h\"\nint main(void) {\n    return twice(21);\n}\n",
			symbol:  "twice", column: 12,
		},
	} {
		t.Run(test.server+"/"+test.file, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			write := func(path, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(root, test.marker), test.flags)
			file := filepath.Join(root, test.file)
			write(file, test.invalid)
			definition := filepath.Join(root, test.helper)
			if test.server == "ty" {
				// Only the project's environment contains this dependency. Using
				// the managed tool's environment would report an unresolved import.
				python := "python3"
				if runtime.GOOS == "windows" {
					python = "python"
				}
				venv := filepath.Join(root, ".venv")
				if output, err := exec.CommandContext(ctx, python, "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
					t.Fatalf("create project environment: %v\n%s", err, output)
				}
				interpreter := filepath.Join(venv, "bin", "python")
				if runtime.GOOS == "windows" {
					interpreter = filepath.Join(venv, "Scripts", "python.exe")
				}
				output, err := exec.CommandContext(ctx, interpreter, "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").Output()
				if err != nil {
					t.Fatal(err)
				}
				definition = filepath.Join(strings.TrimSpace(string(output)), "installed_dependency", "__init__.py")
				write(filepath.Join(filepath.Dir(definition), "py.typed"), "")
			}
			write(definition, test.declaration)
			if _, err := tools.Update(ctx, []devtools.Requirement{{Alternatives: []string{test.server}, Workspace: root}}); err != nil {
				t.Fatal(err)
			}
			service := language.New(root, filepath.Join(t.TempDir(), "graph.json"), lsp.WithManagedTools(tools))
			defer service.Close()
			if test.server == "ty" {
				logFile := filepath.Join(t.TempDir(), "ty.log")
				if err := service.SetServerInitializationOptions("ty", map[string]string{"logFile": logFile, "logLevel": "debug"}); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if t.Failed() {
						contents, _ := os.ReadFile(logFile)
						t.Logf("ty server log:\n%s", contents)
					}
				}()
			}
			server := service.Manager().FindServer(file)
			if server == nil || server.Name != test.server {
				t.Fatalf("server = %+v", server)
			}
			if test.server == "ty" {
				cmd := exec.CommandContext(ctx, server.Command, "check", root, "--output-format", "concise")
				cmd.Dir = root
				output, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(output), "invalid-argument-type") {
					t.Fatalf("ty check did not find the expected type error: %v\n%s", err, output)
				}
			}
			session, err := service.Manager().GetSession(ctx, file)
			if err != nil {
				t.Fatal(err)
			}
			uri, err := session.OpenDocument(ctx, file)
			if err != nil {
				t.Fatal(err)
			}
			diagnostics, known := session.WaitForDiagnostics(ctx, uri, 15*time.Second)
			values := language.DiagnosticsFromProtocol(diagnostics)
			if !known || len(values) == 0 {
				t.Fatalf("expected a type error: known=%v diagnostics=%+v", known, values)
			}
			for _, diagnostic := range values {
				if diagnostic.Range.Start.Line != 2 {
					t.Fatalf("unexpected diagnostic outside the bad call: %+v", diagnostic)
				}
			}
			// Use the LSP session directly so structural fallbacks cannot mask
			// a missing or broken server capability.
			hover, err := session.Hover(ctx, uri, 2, test.column)
			if err != nil || !strings.Contains(language.HoverText(hover), test.symbol) {
				t.Fatalf("hover = %+v, %v", hover, err)
			}
			locations, err := session.DefinitionLocations(ctx, uri, 2, test.column)
			if err != nil || len(locations) == 0 {
				t.Fatalf("definition = %+v, %v; want %s", locations, err, definition)
			}
			location, ok := fileuri.Path(locations[0].URI.String())
			if !ok {
				t.Fatalf("invalid definition URI: %s", locations[0].URI)
			}
			actual, err := filepath.EvalSymlinks(location)
			expected, expectedErr := filepath.EvalSymlinks(definition)
			if err != nil || expectedErr != nil || actual != expected {
				t.Fatalf("definition = %s, want %s (%v, %v)", actual, expected, err, expectedErr)
			}
			items, err := session.CompletionItems(ctx, uri, 2, test.column, nil)
			if err != nil || len(items.Items) == 0 {
				t.Fatalf("completion = %+v, %v", items, err)
			}
			if _, err := session.SyncDocument(ctx, file, test.valid); err != nil {
				t.Fatal(err)
			}
			diagnostics, known = session.WaitForDiagnostics(ctx, uri, 15*time.Second)
			if !known || len(diagnostics) != 0 {
				t.Fatalf("corrected unsaved buffer: known=%v diagnostics=%+v", known, diagnostics)
			}
			contents, err := os.ReadFile(file)
			if err != nil || string(contents) != test.invalid {
				t.Fatalf("unsaved edit changed disk content: %v", err)
			}
		})
	}
}
