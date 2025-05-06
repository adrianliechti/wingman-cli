package rag

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
	"github.com/adrianliechti/wingman/pkg/index"
)

func IndexDir(ctx context.Context, client *wingman.Client, i index.Provider, root string) error {
	supported := []string{
		".csv",
		".md",
		".rst",
		".tsv",
		".txt",

		".pdf",

		// ".jpg", ".jpeg",
		// ".png",
		// ".bmp",
		// ".tiff",
		// ".heif",

		".docx",
		".pptx",
		".xlsx",
	}

	var cursor string

	mapping := make(map[string]string)
	candidates := make(map[string]string)

	for {
		page, err := i.List(ctx, &index.ListOptions{
			Cursor: cursor,
		})

		if err != nil {
			return err
		}

		cursor = page.Cursor

		if len(page.Items) == 0 {
			break
		}

		for _, i := range page.Items {
			path := i.Metadata["path"]
			revision := i.Metadata["revision"]

			mapping[path] = i.ID
			candidates[path] = revision
		}
	}

	var result error

	revisions := make(map[string]string)

	filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		if e.IsDir() || !slices.Contains(supported, filepath.Ext(path)) {
			return nil
		}

		data, err := os.ReadFile(path)

		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		name := filepath.Base(path)

		md5_hash := md5.Sum(data)
		md5_text := hex.EncodeToString(md5_hash[:])

		revisions["/"+rel] = md5_text

		if revision, ok := candidates["/"+rel]; ok {
			if strings.EqualFold(revision, md5_text) {
				return nil
			}
		}

		cli.Infof("Indexing /%s...", rel)

		extraction, err := client.Extractions.New(ctx, wingman.ExtractionRequest{
			Name:   name,
			Reader: bytes.NewReader(data),
		})

		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		segments, err := client.Segments.New(ctx, wingman.SegmentRequest{
			Name:   "content.txt",
			Reader: strings.NewReader(extraction.Text),

			SegmentLength:  wingman.Ptr(3000),
			SegmentOverlap: wingman.Ptr(1500),
		})

		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		var documents []index.Document

		for i, segment := range segments {
			document := index.Document{
				Content: segment.Text,

				Metadata: map[string]string{
					"path": "/" + rel,

					"index":    fmt.Sprintf("%d", i),
					"revision": md5_text,
				},
			}

			documents = append(documents, document)
		}

		if err := i.Index(ctx, documents...); err != nil {
			result = errors.Join(result, err)
			return nil
		}

		return nil
	})

	var deletions []string

	for path := range candidates {
		if _, ok := revisions[path]; ok {
			continue
		}

		id, found := mapping[path]

		if !found {
			continue
		}

		deletions = append(deletions, id)
	}

	if len(deletions) > 0 {
		if err := i.Delete(ctx, deletions...); err != nil {
			result = errors.Join(result, err)
		}
	}

	return result
}
