package rag

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/hnsw"
	"github.com/google/uuid"

	"github.com/adrianliechti/wingman-cli/app/agent"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

func Run(ctx context.Context, client *wingman.Client, model string) error {
	root, err := filepath.Abs("")

	if err != nil {
		return err
	}

	index := NewIndex(client)
	index.Import(filepath.Join(root, ".cache", "index"))

	IndexDir(ctx, client, index, root)
	index.Export(filepath.Join(root, ".cache", "index"))

	tools := []tool.Tool{
		{
			Name:        "retrieve_documents",
			Description: "Query the knowledge base to find relevant documents to answer questions",

			Schema: map[string]any{
				"type": "object",

				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language query input. The query input should be clear and standalone",
					},
				},

				"required": []string{"query"},
			},

			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				data, err := json.Marshal(args)

				if err != nil {
					return nil, err
				}

				var parameters struct {
					Query string `json:"query"`
				}

				if err := json.Unmarshal(data, &parameters); err != nil {
					return nil, err
				}

				result, err := index.Query(ctx, parameters.Query)

				if err != nil {
					return nil, err
				}

				var texts []string

				for _, document := range result {
					texts = append(texts, document.Content)
				}

				return texts, nil
			},
		},
	}

	return agent.Run(ctx, client, model, tools, &agent.RunOptions{})
}

type LocalIndex struct {
	client *wingman.Client

	graph     *hnsw.Graph[string]
	documents map[string]Document
}

func NewIndex(client *wingman.Client) *LocalIndex {
	graph := hnsw.NewGraph[string]()

	return &LocalIndex{
		client: client,

		graph:     graph,
		documents: make(map[string]Document),
	}
}

func (i *LocalIndex) Import(name string) error {
	f, err := os.Open(name)

	if err != nil {
		return err
	}

	defer f.Close()

	var documents []Document

	if err := json.NewDecoder(f).Decode(&documents); err != nil {
		return err
	}

	for _, document := range documents {
		id := document.ID

		if id == "" {
			id = uuid.New().String()
		}

		i.documents[id] = document
		i.graph.Add(hnsw.MakeNode(id, document.Embedding))
	}

	return nil
}

func (i *LocalIndex) Export(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)

	if err != nil {
		return err
	}

	defer f.Close()

	documents := slices.Collect(maps.Values(i.documents))

	return json.NewEncoder(f).Encode(documents)
}

func (i *LocalIndex) List(ctx context.Context) ([]Document, error) {
	return slices.Collect(maps.Values(i.documents)), nil
}

func (i *LocalIndex) Index(ctx context.Context, documents ...Document) error {
	for _, document := range documents {
		id := uuid.New().String()

		i.documents[id] = document
		i.graph.Add(hnsw.MakeNode(id, document.Embedding))
	}

	return nil
}

func (i *LocalIndex) Delete(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		delete(i.documents, id)
		i.graph.Delete(id)
	}

	return nil
}

func (i *LocalIndex) Query(ctx context.Context, query string) ([]Result, error) {
	model := ""
	topK := 10

	embedding, err := i.client.Embeddings.New(ctx, wingman.EmbeddingsRequest{
		Model: model,
		Texts: []string{query},
	})

	if err != nil {
		return nil, err
	}

	var result []Result

	for _, node := range i.graph.Search(embedding.Embeddings[0], topK) {
		document, ok := i.documents[node.Key]

		if !ok {
			continue
		}

		result = append(result, document)
	}

	return result, nil
}

type Index interface {
	List(ctx context.Context) ([]Document, error)

	Index(ctx context.Context, documents ...Document) error
	Delete(ctx context.Context, ids ...string) error

	Query(ctx context.Context, query string) ([]Result, error)
}

type Document = wingman.Document
type Result = wingman.Document

func IndexDir(ctx context.Context, client *wingman.Client, index Index, root string) error {
	model := ""

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

	var result error

	revisions := map[string]string{}

	filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		if strings.Contains(path, ".cache") {
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

		md5_hash := md5.Sum(data)
		md5_text := hex.EncodeToString(md5_hash[:])

		cachedir := filepath.Join(root, ".cache", md5_text[0:2], md5_text[2:4], md5_text)
		os.MkdirAll(cachedir, 0755)

		info, err := e.Info()

		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		rel, _ := filepath.Rel(root, path)

		name := filepath.Base(path)
		title := strings.TrimSuffix(name, filepath.Ext(name))
		revision := md5_text

		metadata := Metadata{
			Name: filepath.Base(path),
			Path: "/" + rel,

			Title:    title,
			Revision: revision,

			Size: info.Size(),
			Time: info.ModTime(),
		}

		if !exists(cachedir, "metadata.json") {
			if err := writeJSON(cachedir, "metadata.json", metadata); err != nil {
				result = errors.Join(result, err)
				return nil
			}
		}

		if !exists(cachedir, "content.txt") {
			extraction, err := client.Extractions.New(ctx, wingman.ExtractionRequest{
				Name:   metadata.Name,
				Reader: bytes.NewReader(data),
			})

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			if err := writeData(cachedir, "content.txt", []byte(extraction.Text)); err != nil {
				result = errors.Join(result, err)
				return nil
			}
		}

		if !exists(cachedir, "embeddings.json") {
			data, err := readData(cachedir, "content.txt")

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			segments, err := client.Segments.New(ctx, wingman.SegmentRequest{
				Name:   "content.txt",
				Reader: bytes.NewReader(data),

				SegmentLength:  wingman.Ptr(3000),
				SegmentOverlap: wingman.Ptr(1500),
			})

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			embeddings := Embeddings{
				Model: model,
			}

			titleEmbedding, err := client.Embeddings.New(ctx, wingman.EmbeddingsRequest{
				Model: model,
				Texts: []string{title},
			})

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			embeddings.Segments = append(embeddings.Segments, Segment{
				Text:      title,
				Embedding: titleEmbedding.Embeddings[0],
			})

			for _, segment := range segments {
				segmentEmbedding, err := client.Embeddings.New(ctx, wingman.EmbeddingsRequest{
					Model: model,
					Texts: []string{segment.Text},
				})

				if err != nil {
					result = errors.Join(result, err)
					return nil
				}

				embeddings.Segments = append(embeddings.Segments, Segment{
					Text:      segment.Text,
					Embedding: segmentEmbedding.Embeddings[0],
				})
			}

			if err := writeJSON(cachedir, "embeddings.json", embeddings); err != nil {
				result = errors.Join(result, err)
				return nil
			}
		}

		if !exists(cachedir, "documents.json") {
			var embeddings Embeddings

			if err := readJSON(cachedir, "embeddings.json", &embeddings); err != nil {
				result = errors.Join(result, err)
				return nil
			}

			var documents []Document

			for i, segment := range embeddings.Segments {
				document := Document{
					Title:  metadata.Title,
					Source: fmt.Sprintf("%s#%d", metadata.Path, i+1),

					Content:   segment.Text,
					Embedding: segment.Embedding,

					Metadata: map[string]string{
						"filename": metadata.Name,
						"filepath": metadata.Path,

						"index":    fmt.Sprintf("%d", i),
						"revision": metadata.Revision,
					},
				}

				if err := index.Index(ctx, document); err != nil {
					result = errors.Join(result, err)
					return nil
				}

				documents = append(documents, document)
			}

			if err != writeJSON(cachedir, "documents.json", documents) {
				result = errors.Join(result, err)
				return nil
			}
		}

		revisions[metadata.Path] = metadata.Revision

		return nil
	})

	if index != nil {
		list, err := index.List(ctx)

		if err != nil {
			return err
		}

		var deletions []string

		for _, d := range list {
			filepath := d.Metadata["filepath"]
			revision := d.Metadata["revision"]

			if filepath == "" || revision == "" {
				continue
			}

			ref := revisions[filepath]

			if strings.EqualFold(revision, ref) {
				continue
			}

			deletions = append(deletions, d.ID)
		}

		if len(deletions) > 0 {
			if err := index.Delete(ctx, deletions...); err != nil {
				return err
			}
		}
	}

	return result
}

type Metadata struct {
	Name string `json:"name"`
	Path string `json:"path"`

	Title string `json:"title"`

	Revision string `json:"revision"`

	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

type Embeddings struct {
	Model string `json:"model"`

	Segments []Segment `json:"segments"`
}

type Segment struct {
	Text string `json:"text"`

	Embedding []float32 `json:"embedding"`
}

func exists(path, name string) bool {
	info, err := os.Stat(filepath.Join(path, name))

	if err != nil {
		if os.IsNotExist(err) {
			return false
		}

		return false
	}

	return !info.IsDir()
}

func readData(dir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, name))
}

func readText(dir, name string) (string, error) {
	data, err := readData(dir, name)

	if err != nil {
		return "", err
	}

	return string(data), nil
}

func readJSON(dir, name string, v any) error {
	data, err := readData(dir, name)

	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

func writeData(dir, name string, data []byte) error {
	return os.WriteFile(filepath.Join(dir, name), data, 0644)
}

func writeJSON(dir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")

	if err != nil {
		return err
	}

	return writeData(dir, name, data)
}
