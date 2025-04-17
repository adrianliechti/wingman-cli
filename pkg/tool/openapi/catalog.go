package openapi

import (
	"context"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/adrianliechti/wingman-cli/pkg/rest"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func New(path string, client *rest.Client) (*Catalog, error) {
	doc, err := parse(path)

	if err != nil {
		return nil, err
	}

	operations, err := getOperations(doc)

	if err != nil {
		return nil, err
	}

	return &Catalog{
		doc: doc,

		client: client,

		operations: operations,
	}, nil
}

var (
	_ tool.Provider = (*Catalog)(nil)
)

type Catalog struct {
	doc *openapi3.T

	client *rest.Client

	operations map[string]Operation
}

func (c *Catalog) Tools(ctx context.Context) ([]tool.Tool, error) {
	var tools []tool.Tool

	for _, o := range c.operations {
		tool := tool.Tool{
			Name:        o.Name,
			Description: o.Description,

			Schema: o.Schema,

			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				return o.Execute(ctx, c.client, args)
			},
		}

		tools = append(tools, tool)
	}

	return tools, nil
}

func getOperations(doc *openapi3.T) (map[string]Operation, error) {
	result := map[string]Operation{}

	for p, path := range doc.Paths.Map() {
		operations := path.Operations()

		for m, o := range operations {
			if o.OperationID == "" {
				continue
			}

			name := camelToSnake(o.OperationID)

			description := o.Summary
			description = strings.TrimRight(description, ". \n")
			description += "."

			if o.Description != "" {
				description += " " + o.Description
				description = strings.TrimRight(description, ". \n")
				description += "."
			}

			queries := []string{}

			required := []string{}
			properties := map[string]any{}

			for _, p := range o.Parameters {
				if p.Value == nil {
					continue
				}

				if strings.EqualFold(p.Value.In, "query") {
					query := strings.ToLower(p.Value.Name)
					queries = append(queries, query)
				} else if strings.EqualFold(p.Value.In, "path") {
					//
				} else if strings.EqualFold(p.Value.In, "header") {
					continue
				} else {
					// unknown
					continue
				}

				name := p.Value.Name
				//types := p.Value.Schema.Value.Type.Slice()

				property := map[string]any{
					"type":        "string",
					"description": p.Value.Description,
				}

				// if len(types) == 0 {
				// 	definition["type"] = types[0]
				// }

				// if len(types) > 1 {
				// 	definition["type"] = types
				// }

				properties[name] = property

				if p.Value.Required {
					required = append(required, name)
				}
			}

			var contentType string

			if o.RequestBody != nil {
				content := o.RequestBody.Value.Content.Get("application/json")

				if content != nil {
					contentType = "application/json"

					properties["body"] = map[string]any{
						"type":       "object",
						"properties": content.Schema.Value.Properties,
					}

					required = append(required, "body")
				}
			}

			if len(properties) == 0 {
				properties["body"] = map[string]any{}
			}

			schema := map[string]any{
				"type": "object",

				"properties": properties,
			}

			if len(required) > 0 {
				schema["required"] = required
			}

			result[name] = Operation{
				Name:        name,
				Description: description,

				Method: m,
				Path:   p,

				Queries: queries,

				Type:   contentType,
				Schema: schema,
			}
		}
	}

	return result, nil
}

func camelToSnake(s string) string {
	var result strings.Builder

	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteRune('_')
			}

			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}

	return result.String()
}
