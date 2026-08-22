package language_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/devtools"
	"github.com/adrianliechti/wingman-agent/pkg/language"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func TestLiveReactTypeScriptDiagnostics(t *testing.T) {
	if os.Getenv("WINGMAN_LIVE_LSP") == "" {
		t.Skip("set WINGMAN_LIVE_LSP=1 to run a real TypeScript language server")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "debug", "react-vite"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "src", "main.tsx")
	tools, err := devtools.New()
	if err != nil {
		t.Fatal(err)
	}
	service := language.New(root, filepath.Join(t.TempDir(), "graph.json"), lsp.WithCommandResolver(tools.Resolve))
	defer service.Close()
	server := service.Manager().FindServer(file)
	if server == nil {
		t.Fatal("no TypeScript language server detected")
	}
	t.Logf("server=%s command=%s", server.Name, server.Command)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	diagnostics, known, err := service.FileDiagnostics(ctx, file, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("TypeScript diagnostics were not ready")
	}
	for _, diagnostic := range diagnostics {
		t.Errorf("unexpected diagnostic: %+v", diagnostic)
	}
}
