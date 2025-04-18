package retriever

import (
	"context"
	"encoding/json"

	"github.com/adrianliechti/wingman-cli/pkg/index"
	"github.com/adrianliechti/wingman-cli/pkg/tool"

	wingman "github.com/adrianliechti/wingman/pkg/client"
)

type Retriever struct {
	index  *index.Index
	client *wingman.Client
}

func New(client *wingman.Client, index *index.Index) *Retriever {
	return &Retriever{
		index:  index,
		client: client,
	}
}

func (r *Retriever) Tools(ctx context.Context) ([]tool.Tool, error) {
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

				embeddings, err := r.client.Embeddings.New(ctx, wingman.EmbeddingsRequest{
					Texts: []string{parameters.Query},
				})

				if err != nil {
					return nil, err
				}

				vector := embeddings.Embeddings[0]

				documents, err := r.index.Search(ctx, vector, 10)

				if err != nil {
					return nil, err
				}

				var texts []string

				for _, d := range documents {
					texts = append(texts, d.Text)
				}

				return texts, nil
			},
		},
	}

	return tools, nil
}
