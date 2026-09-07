package debugadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

func TestNativeAdapterFindsDefinitionsWithoutMatchingCommentsOrDeclarations(t *testing.T) {
	for _, extension := range []string{".c", ".cpp", ".cc", ".cxx", ".c++", ".C"} {
		t.Run(extension, func(t *testing.T) {
			for _, source := range []string{
				"int main(void) { return 0; }",
				"int\nmain(int argc, char **argv)\n{ return argc; }",
				"auto main() -> int { return 0; }",
			} {
				targets, err := NewRegistry().DetectFile("program"+extension, []byte(source))
				if err != nil || len(targets) != 1 || targets[0].Language != cLanguage || targets[0].Name != "main" {
					t.Fatalf("DetectFile(%q) = %+v, %v", source, targets, err)
				}
			}
			for _, source := range []string{
				"// int main(void) {}\nint helper(void) { return 0; }",
				"/* int main(void) {} */",
				`const char *example = "int main(void) {}";`,
				"int main(void);",
				"int main_helper(void) { return 0; }",
			} {
				targets, err := NewRegistry().DetectFile("library"+extension, []byte(source))
				if err != nil || len(targets) != 0 {
					t.Fatalf("non-entry source produced targets: %+v, %v", targets, err)
				}
			}
		})
	}
}

func copyNativeExample(t *testing.T, language string) (string, Target) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, language+" project")
	mainPath := "main.c"
	if language == "C" {
		if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "examples", "c"))); err != nil {
			t.Fatal(err)
		}
	} else {
		mainPath = "main.cpp"
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		for path, content := range map[string]string{
			"main.cpp":          "#include <iostream>\n\n#include \"arithmetic.h\"\n\nint main() {\n    const int result = add(20, 22);\n    std::cout << \"20 + 22 = \" << result << std::endl;\n    return 0;\n}\n",
			"arithmetic.h":      "int add(int left, int right);\n",
			"arithmetic.cpp":    "#include \"arithmetic.h\"\n\nint add(int left, int right) {\n    return left + right;\n}\n",
			"compile_flags.txt": "-std=c++17\n-Wall\n-Wextra\n",
		} {
			if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	source, err := os.ReadFile(filepath.Join(root, mainPath))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := NewRegistry().DetectFile(mainPath, source)
	if err != nil || len(targets) != 1 || targets[0].Line != 5 {
		t.Fatalf("example target = %+v, %v", targets, err)
	}
	return root, targets[0]
}

func TestNativeExamplePlanBuildsHelpersAndRebuildsChangedSources(t *testing.T) {
	for _, language := range []string{"C", "C++"} {
		t.Run(language, func(t *testing.T) {
			if cCompiler(language == "C++") == "" {
				t.Skipf("a %s compiler is required", language)
			}
			root, target := copyNativeExample(t, language)
			extension := filepath.Ext(target.Path)
			registry := NewRegistry()
			request := Request{Action: "debug", WorkspaceDir: root, ProjectDir: ".", Target: target}
			plan, err := registry.Plan(cLanguage, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Breakpoints) != 1 || plan.Breakpoints[0].Line != 5 || !plan.SupportsTerminal {
				t.Fatalf("example debug plan = %+v", plan)
			}
			program := filepath.Join(root, plan.Configuration["program"].(string))
			if _, err := os.Stat(program); !os.IsNotExist(err) {
				t.Fatal("planning executed a build")
			}
			// Another program in the directory must not be linked into this one.
			if err := os.WriteFile(filepath.Join(root, "other"+extension), []byte("int main(void) { return 1; }"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"42", "43"} {
				plan, err := registry.Plan(cLanguage, request)
				if err != nil || plan.PreLaunch == nil || !plan.PreLaunch.WaitForExit {
					t.Fatalf("build plan = %+v, %v", plan, err)
				}
				build := exec.Command(plan.PreLaunch.Command, plan.PreLaunch.Args...)
				build.Dir = root
				if output, err := build.CombinedOutput(); err != nil {
					t.Fatalf("build %s example: %v\n%s", language, err, output)
				}
				if output, err := exec.Command(program).CombinedOutput(); err != nil || !strings.Contains(string(output), "20 + 22 = "+want) {
					t.Fatalf("example output = %q, %v; want %s", output, err, want)
				}
				if err := os.WriteFile(filepath.Join(root, "arithmetic"+extension), []byte("int add(int left, int right) { return left + right + 1; }"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			request.Action = "run"
			plan, err = registry.Plan(cLanguage, request)
			if err != nil || plan.Configuration["noDebug"] != true || len(plan.Breakpoints) != 0 {
				t.Fatalf("run plan = %+v, %v", plan, err)
			}
		})
	}
}

func TestNativeExampleRequestsLLDBWithoutAnInstalledAdapter(t *testing.T) {
	for _, language := range []string{"C", "C++"} {
		t.Run(language, func(t *testing.T) {
			root, _ := copyNativeExample(t, language)
			requirements, err := dap.DetectRequirements(context.Background(), root, NewRegistry().Descriptors())
			if err != nil {
				t.Fatal(err)
			}
			if len(requirements) != 1 || requirements[0].Language != cLanguage || requirements[0].Name != "codelldb-native" || len(requirements[0].Commands) != 1 || requirements[0].Commands[0] != "codelldb" {
				t.Fatalf("native debugger requirements = %+v", requirements)
			}
		})
	}
}

func TestLiveCodeLLDBNativeExample(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_DAP") == "" {
		t.Skip("set WINGMAN_LIVE_DAP=1 to run real C/C++ debug sessions")
	}
	for _, language := range []string{"C", "C++"} {
		t.Run(language, func(t *testing.T) {
			if cCompiler(language == "C++") == "" {
				t.Skipf("a %s compiler is required", language)
			}
			root, target := copyNativeExample(t, language)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			resolver := liveAdapterResolver(t, ctx, root, "codelldb")
			registry := NewRegistry()
			plan, err := registry.Plan(cLanguage, Request{Action: "debug", WorkspaceDir: root, ProjectDir: ".", Target: target})
			if err != nil {
				t.Fatal(err)
			}
			manager := dap.NewManager(root, resolver, registry.Descriptors()...)
			defer manager.Close()
			helper := filepath.Join(root, "arithmetic"+filepath.Ext(target.Path))
			session, err := manager.Start(ctx, dap.StartOptions{
				Adapter: "codelldb-native", ProjectDir: plan.ProjectDir, Configuration: plan.Configuration, PreLaunch: plan.PreLaunch,
				Breakpoints: map[string][]dap.SourceBreakpoint{helper: {{Line: 4}}},
			})
			if err != nil {
				t.Fatalf("start %s debugger: %v\n%s", language, err, failedSessionOutput(manager))
			}
			status := session.Status()
			if status.State != dap.StateStopped {
				status, _ = session.WaitForStop(ctx, 0)
			}
			if status.State != dap.StateStopped {
				t.Fatalf("%s program did not stop: %+v\n%s", language, status, session.Output())
			}
			frames, _, err := session.StackTrace(ctx, status.Stop.ThreadID, 0, 5)
			if err != nil || len(frames) == 0 || frames[0].Source == nil || !sameFile(frames[0].Source.Path, helper) || frames[0].Line != 4 {
				t.Fatalf("native breakpoint frames = %+v, %v", frames, err)
			}
			value, err := session.EvaluateContext(ctx, "left", frames[0].ID, "watch")
			if err != nil || strings.TrimSpace(value.Result) != "20" {
				t.Fatalf("native variable = %+v, %v", value, err)
			}
			if err := session.Continue(ctx, status.Stop.ThreadID); err != nil {
				t.Fatal(err)
			}
			status, _ = session.WaitForStop(ctx, status.StateVersion)
			if status.State != dap.StateTerminated || !strings.Contains(session.Output(), "20 + 22 = 42") {
				t.Fatalf("%s program did not complete: %+v\n%s", language, status, session.Output())
			}
		})
	}
}
