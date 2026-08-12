package code

import (
	"context"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

type lspResolver struct {
	ws *Workspace
}

func (r *lspResolver) ResolveCall(ctx context.Context, file string, line, column int) (string, int, bool) {
	abs := filepath.Join(r.ws.RootPath, filepath.FromSlash(file))
	if !r.ws.hasLSPServerFor(abs) {
		return "", 0, false
	}

	var defs []lsp.DefLocation
	err := r.ws.withLSPDocument(ctx, abs, nil, func(session *lsp.Session, uri string) error {
		var err error
		defs, err = session.DefinitionLocations(ctx, uri, line, column)
		return err
	})
	if err != nil || len(defs) == 0 {
		return "", 0, false
	}

	rel, err := filepath.Rel(r.ws.RootPath, defs[0].Path)
	if err != nil {
		return "", 0, false
	}
	return filepath.ToSlash(rel), defs[0].Line + 1, true
}
