package retriever

import (
	"context"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman/pkg/index"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

type Retriever struct {
	index index.Provider
}

func New(index index.Provider) *Retriever {
	return &Retriever{
		index: index,
	}
}

func (r *Retriever) Tools(ctx context.Context) ([]tool.Tool, error) {
	tools := []tool.Tool{
		{
			Name:        "retrieve_documents",
			Description: "Query the knowledge base to find relevant documents to answer questions",

			Schema: &jsonschema.Schema{
				Type: "object",

				Properties: map[string]*jsonschema.Schema{
					"query": {
						Type:        "string",
						Description: "The natural language query input. The query input should be clear and standalone",
					},
				},

				Required: []string{"query"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				query := params["query"].(string)

				limit := 5

				documents, err := r.index.Query(ctx, query, &index.QueryOptions{
					Limit: &limit,
				})

				if err != nil {
					return nil, err
				}

				var texts []string

				for _, d := range documents {
					texts = append(texts, d.Content)
				}

				return texts, nil
			},
		},
	}

	return tools, nil
}
