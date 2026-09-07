package devtools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

func TestPythonNativeExecutableSurvivesActivation(t *testing.T) {
	if os.Getenv("WINGMAN_TEST_NATIVE_COMMAND") == "1" {
		fmt.Println("native-command-ready")
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		binary  []byte
		wantErr bool
	}{
		{name: "native wheel", binary: native},
		{name: "script without entry point", binary: []byte("#!/bin/sh\nexit 0\n"), wantErr: true},
		{name: "invalid executable", binary: []byte("not an executable"), wantErr: true},
		{name: "missing executable", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := newManager(t.TempDir())
			manager.install = manager.installRecipe
			manager.look = func(string) (string, error) { return "/python", nil }
			index := "https://packages.example.invalid/artifactory/api/pypi/tools/simple"
			t.Setenv("PIP_INDEX_URL", index)
			var stage, bin string
			manager.run = func(_ context.Context, _ string, args []string, _ string, env []string) ([]byte, error) {
				if len(args) >= 3 && args[0] == "-m" && args[1] == "venv" {
					stage = args[2]
					bin = filepath.Join(stage, "bin")
					if runtime.GOOS == "windows" {
						bin = filepath.Join(stage, "Scripts")
					}
					return nil, os.MkdirAll(bin, 0o755)
				}
				if len(args) >= 3 && args[0] == "-m" && args[1] == "pip" {
					if !slices.Contains(args, "--only-binary=:all:") || args[len(args)-1] != "ty" {
						t.Fatalf("pip arguments = %v", args)
					}
					if !slices.Contains(env, "PIP_INDEX_URL="+index) {
						t.Fatal("pip did not inherit the enterprise index")
					}
					if test.binary == nil {
						return nil, nil
					}
					return nil, os.WriteFile(filepath.Join(bin, tooling.Candidates(runtime.GOOS, "ty")[0]), test.binary, 0o755)
				}
				if len(args) == 2 && args[0] == "-c" {
					return []byte(`{}`), nil
				}
				return nil, fmt.Errorf("unexpected Python command: %v", args)
			}
			changed, err := manager.Update(context.Background(), []Requirement{{Alternatives: []string{"ty"}}})
			if test.wantErr {
				if err == nil || changed || manager.Resolve("ty") != "" {
					t.Fatalf("invalid wheel was activated: changed=%v err=%v", changed, err)
				}
				return
			}
			if err != nil || !changed {
				t.Fatalf("Update = %v, %v", changed, err)
			}
			if _, err := os.Stat(stage); !os.IsNotExist(err) {
				t.Fatalf("staging directory still exists: %v", err)
			}
			command := manager.Resolve("ty")
			cmd := exec.Command(command, "-test.run=^TestPythonNativeExecutableSurvivesActivation$")
			cmd.Env = append(os.Environ(), "WINGMAN_TEST_NATIVE_COMMAND=1")
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "native-command-ready") {
				t.Fatalf("activated native executable: %v\n%s", err, output)
			}
		})
	}
}
