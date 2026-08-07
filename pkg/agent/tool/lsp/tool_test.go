package lsp_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/lsp"
	corelsp "github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestNewToolsExposesSingleLSPTool(t *testing.T) {
	tools := NewTools(nil)
	if len(tools) != 1 {
		t.Fatalf("len(NewTools) = %d, want 1", len(tools))
	}

	lspTool := tools[0]
	if lspTool.Name != "lsp" {
		t.Fatalf("tool name = %q, want lsp", lspTool.Name)
	}

	props := lspTool.Parameters["properties"].(map[string]any)
	operation := props["operation"].(map[string]any)
	enums := operation["enum"].([]string)

	for _, want := range []string{
		"diagnostics",
		"goToDefinition",
		"findReferences",
		"hover",
		"documentSymbol",
		"workspaceSymbol",
		"goToImplementation",
		"incomingCalls",
		"outgoingCalls",
	} {
		if !slices.Contains(enums, want) {
			t.Fatalf("operation enum missing %q: %#v", want, enums)
		}
	}

	for _, removed := range []string{"workspaceDiagnostics", "prepareCallHierarchy"} {
		if slices.Contains(enums, removed) {
			t.Fatalf("operation enum should not contain %q: %#v", removed, enums)
		}
	}

	if _, ok := props["symbol"]; !ok {
		t.Fatal("expected symbol parameter")
	}

	for _, key := range []string{"line", "column"} {
		param := props[key].(map[string]any)
		if minimum, ok := param["minimum"].(int); !ok || minimum != 1 {
			t.Fatalf("%s minimum = %#v, want 1", key, param["minimum"])
		}
	}

	required := lspTool.Parameters["required"].([]string)
	if !slices.Equal(required, []string{"operation"}) {
		t.Fatalf("required = %#v, want [operation]", required)
	}

	additional, ok := lspTool.Parameters["additionalProperties"].(bool)
	if !ok || additional {
		t.Fatalf("additionalProperties = %#v, want false", lspTool.Parameters["additionalProperties"])
	}
}

func TestLSPToolRejectsZeroBasedPositionInput(t *testing.T) {
	manager := newTestManager(t)
	lspTool := NewTools(manager)[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      0,
		"column":    1,
	})
	if err == nil || !strings.Contains(err.Error(), "line must be a positive 1-based integer") {
		t.Fatalf("expected line validation error, got: %v", err)
	}

	_, err = lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      1,
		"column":    0,
	})
	if err == nil || !strings.Contains(err.Error(), "column must be a positive 1-based integer") {
		t.Fatalf("expected column validation error, got: %v", err)
	}
}

func TestLSPToolRejectsFractionalPositionInput(t *testing.T) {
	manager := newTestManager(t)
	lspTool := NewTools(manager)[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      1.5,
		"column":    1,
	})
	if err == nil || !strings.Contains(err.Error(), "line must be a positive 1-based integer") {
		t.Fatalf("expected line validation error, got: %v", err)
	}
}

func TestLSPToolRequiresPositionOrSymbol(t *testing.T) {
	manager := newTestManager(t)
	lspTool := NewTools(manager)[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
	})
	if err == nil || !strings.Contains(err.Error(), "position required") {
		t.Fatalf("expected position required error, got: %v", err)
	}

	_, err = lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      1,
	})
	if err == nil || !strings.Contains(err.Error(), "column must be a positive 1-based integer") {
		t.Fatalf("expected column error, got: %v", err)
	}

	_, err = lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"column":    3,
	})
	if err == nil || !strings.Contains(err.Error(), "line must be a positive 1-based integer") {
		t.Fatalf("expected line error, got: %v", err)
	}
}

func TestLSPToolResolvesSymbolOnLine(t *testing.T) {
	manager := newTestManager(t)
	lspTool := NewTools(manager)[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      1,
		"symbol":    "missing",
	})
	if err == nil || !strings.Contains(err.Error(), `symbol "missing" not found on line 1`) {
		t.Fatalf("expected symbol-not-on-line error, got: %v", err)
	}

	_, err = lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      1,
		"symbol":    "main",
	})
	if err == nil || !strings.Contains(err.Error(), "no LSP server found") {
		t.Fatalf("expected symbol to resolve and fail at session, got: %v", err)
	}
}

func TestLSPToolRejectsOutOfRangeLine(t *testing.T) {
	manager := newTestManager(t)
	lspTool := NewTools(manager)[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "hover",
		"file_path": "main.go",
		"line":      99,
		"column":    1,
	})
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("expected out of range error, got: %v", err)
	}
}

func TestLSPToolRejectsDirectories(t *testing.T) {
	manager := corelsp.NewManager(t.TempDir())
	lspTool := NewTools(manager)[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "documentSymbol",
		"file_path": ".",
	})
	if err == nil || !strings.Contains(err.Error(), "path is a directory") {
		t.Fatalf("expected directory validation error, got: %v", err)
	}
}

func TestLSPToolRejectsPathsOutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	lspTool := NewTools(corelsp.NewManager(tmpDir))[0]

	_, err := lspTool.Execute(context.Background(), map[string]any{
		"operation": "documentSymbol",
		"file_path": outside,
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected outside workspace error, got: %v", err)
	}

	_, err = lspTool.Execute(context.Background(), map[string]any{
		"operation": "documentSymbol",
		"file_path": "../outside.go",
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected relative outside workspace error, got: %v", err)
	}
}

func newTestManager(t *testing.T) *corelsp.Manager {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	return corelsp.NewManager(tmpDir)
}
