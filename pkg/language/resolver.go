package language

import (
	"context"
	"path/filepath"

	"github.com/adrianliechti/wingman-agent/pkg/fileuri"
	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

type lspResolver struct {
	service *Service
}

func (r *lspResolver) ResolveCall(ctx context.Context, file string, line, column int) (string, int, bool) {
	absPath := filepath.Join(r.service.root, filepath.FromSlash(file))
	if !r.service.hasLSPServerFor(absPath) {
		return "", 0, false
	}
	var values []lsp.Location
	err := r.service.withLSPDocument(ctx, absPath, nil, func(session *lsp.Session, uri string) error {
		var err error
		values, err = session.DefinitionLocations(ctx, uri, line, column)
		return err
	})
	if err != nil {
		return "", 0, false
	}
	if len(values) == 0 {
		return "", 0, false
	}
	path, ok := fileuri.Path(values[0].URI.String())
	if !ok {
		return "", 0, false
	}
	relative, err := filepath.Rel(r.service.root, path)
	if err != nil {
		return "", 0, false
	}
	return filepath.ToSlash(relative), int(values[0].Range.Start.Line) + 1, true
}
