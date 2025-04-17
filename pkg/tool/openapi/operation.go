package openapi

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/rest"
)

type Operation struct {
	Name        string
	Description string

	Method string
	Path   string

	Queries []string

	Type   string
	Schema map[string]any
}

func (o *Operation) Execute(ctx context.Context, client *rest.Client, parameters map[string]any) (string, error) {
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

	data, err := client.Execute(ctx, o.Method, path, o.Type, body)

	if err != nil {
		return "", err
	}

	return string(data), nil
}
