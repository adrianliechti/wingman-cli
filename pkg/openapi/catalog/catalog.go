package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/adrianliechti/wingman/pkg/openapi/client"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"github.com/getkin/kin-openapi/openapi3"
)

type Catalog struct {
	doc *openapi3.T

	api *client.Client
	llm *openai.Client

	operations map[string]Operation

	messages []openai.ChatCompletionMessageParamUnion
}

type Client interface {
	Execute(ctx context.Context, method, path string, body io.Reader) ([]byte, string)
}

func New(path string, api *client.Client, llm *openai.Client) (*Catalog, error) {
	doc, err := parse(path)

	if err != nil {
		return nil, err
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("You are connected to an API Server defined by your Tools. You can interact with it by sending messages. Keep answers short and to the point."),
	}

	operations, err := getOperations(doc)

	if err != nil {
		return nil, err
	}

	return &Catalog{
		doc: doc,

		api: api,
		llm: llm,

		messages:   messages,
		operations: operations,
	}, nil
}

func (c *Catalog) Query(ctx context.Context, model, prompt string) (string, error) {
	result, err := c.invokeLLM(ctx, model, openai.UserMessage(prompt))

	if err != nil {
		return "", err
	}

	for {
		for _, tc := range result.ToolCalls {
			data, err := c.handleToolCall(ctx, tc.Function.Name, tc.Function.Arguments)

			if err != nil {
				return "", err
			}

			result, err = c.invokeLLM(ctx, model, openai.ToolMessage(tc.ID, data))

			if err != nil {
				return "", err
			}
		}

		if len(result.ToolCalls) == 0 {
			break
		}
	}

	return result.Content, nil
}

func (c *Catalog) invokeLLM(ctx context.Context, model string, message openai.ChatCompletionMessageParamUnion) (*openai.ChatCompletionMessage, error) {
	var tools []openai.ChatCompletionToolParam

	for _, o := range c.operations {
		tools = append(tools, openai.ChatCompletionToolParam{
			Type: openai.F(openai.ChatCompletionToolTypeFunction),

			Function: openai.F(shared.FunctionDefinitionParam{
				Name:        openai.F(o.Name),
				Description: openai.F(o.Description),

				Strict: openai.F(true),

				Parameters: openai.F(openai.FunctionParameters(o.Schema)),
			}),
		})
	}

	completion, err := c.llm.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: openai.F(model),

		Tools:    openai.F(tools),
		Messages: openai.F(append(c.messages, message)),
	})

	if err != nil {
		return nil, err
	}

	result := completion.Choices[0].Message

	c.messages = append(c.messages, message, result)

	return &result, nil
}

func (c *Catalog) handleToolCall(ctx context.Context, name string, arguments string) (string, error) {
	o, found := c.operations[name]

	if !found {
		return "", errors.New("function tool not found")
	}

	parameters := map[string]any{}
	json.Unmarshal([]byte(arguments), &parameters)

	path := o.Path

	query := url.Values{}

	for k, v := range parameters {
		key := "{" + k + "}"
		value, ok := v.(string)

		if !ok {
			continue
		}

		path = strings.ReplaceAll(path, key, value)

		if slices.Contains(o.Queries, strings.ToLower(k)) {
			query.Set(k, value)
		}
	}

	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	var body io.Reader

	if val, ok := parameters["body"]; ok {
		data, _ := json.Marshal(val)
		body = strings.NewReader(string(data))
	}

	data, err := c.api.Execute(ctx, o.Method, path, o.Type, body)

	if err != nil {
		return "", err
	}

	return string(data), nil
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

					properties["body"] = content.Schema.Value.Properties
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
