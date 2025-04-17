package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/adrianliechti/go-cli"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func New() (*Client, error) {
	c := &Client{
		client: http.DefaultClient,

		creds: func() azcore.TokenCredential {
			if creds, err := azidentity.NewEnvironmentCredential(nil); err == nil {
				return creds
			}

			if creds, err := azidentity.NewAzureCLICredential(nil); err == nil {
				return creds
			}

			return nil
		}(),

		scopes: []string{"https://graph.microsoft.com/.default"},
	}

	if c.creds == nil {
		return nil, errors.New("unable to configure azure client")
	}

	if _, err := c.Token(context.Background()); err != nil {
		return nil, err
	}

	return c, nil
}

var (
	_ tool.Provider = (*Client)(nil)
)

type Client struct {
	client *http.Client

	creds  azcore.TokenCredential
	scopes []string

	token azcore.AccessToken
}

func (c *Client) Token(ctx context.Context) (azcore.AccessToken, error) {
	t := time.Now().Add(-5 * time.Minute)

	if c.token.ExpiresOn.After(t) {
		return c.token, nil
	}

	token, err := c.creds.GetToken(ctx, policy.TokenRequestOptions{Scopes: c.scopes})

	if err != nil {
		return azcore.AccessToken{}, err
	}

	c.token = token

	return token, nil
}

func (c *Client) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "azure_graph",
			Description: "A tool to call Microsoft Graph API. It supports querying a Microsoft 365 tenant using the Graph API. You only have read-only access.",

			Schema: tool.Schema{
				"type": "object",

				"properties": map[string]any{
					// https://learn.microsoft.com/en-us/graph/use-the-api#version
					"version": map[string]any{
						"type":        "string",
						"description": "The Graph API Version ('v1.0' or 'beta')",
						"default":     "v1.0",
					},

					// https://learn.microsoft.com/en-us/graph/use-the-api#http-methods
					"method": map[string]any{
						"type":        "string",
						"description": "HTTP method to use",

						"enum": []string{
							http.MethodGet,
							http.MethodPost,
							http.MethodPut,
							http.MethodPatch,
							http.MethodDelete,
						},
					},

					"resource": map[string]any{
						"type":        "string",
						"description": "The Graph API URL resource or path to call (e.g. '/me', '/users')",
					},

					"query": map[string]any{
						"type": "object",

						"$filter": map[string]any{
							"type":        "string",
							"description": "Optional OData $filter system query option to filter a collection of resources",
						},

						"$expand": map[string]any{
							"type":        "string",
							"description": "Optional OData $expand system query option to specify related resources or media streams to be included in line with retrieved resources",
						},

						"$select": map[string]any{
							"type":        "string",
							"description": "Optional OData $select system query option to request a specific set of properties for each entity or complex type",
						},

						"$orderby": map[string]any{
							"type":        "string",
							"description": "Optional OData $orderby system query option to request resources in a particular order",
						},

						"$top": map[string]any{
							"type":        "number",
							"description": "Optional OData $top system query option to include a number of items in the queried collection. Set a high value like like 1000 or use paging with $top and $stop to retreive all results",
						},

						"$skip": map[string]any{
							"type":        "number",
							"description": "Optional OData $skip system query option to skip items in the queried collection. Often used in combination with $top for paging",
						},
					},

					"body": map[string]any{
						"type":        "string",
						"description": "The request body (for POST, PUT, PATCH)",
					},
				},

				"required": []string{
					"method",
					"resource",
				},
			},

			Execute: func(ctx context.Context, args map[string]any) (any, error) {
				data, err := json.Marshal(args)

				if err != nil {
					return nil, err
				}

				var parameters struct {
					Version *string `json:"version"`

					Method   string `json:"method"`
					Resource string `json:"resource"`

					Query *struct {
						Filter  *string `json:"$filter"`
						Expand  *string `json:"$expand"`
						Select  *string `json:"$select"`
						OrderBy *string `json:"$orderby"`
						Top     *int    `json:"$top"`
						Skip    *int    `json:"$skip"`
					} `json:"query"`

					Body *string `json:"body"`
				}

				if err := json.Unmarshal(data, &parameters); err != nil {
					return nil, err
				}

				var version string

				if parameters.Version != nil {
					version = *parameters.Version
				}

				if version == "" {
					version = "v1.0"
				}

				resource := strings.TrimLeft(parameters.Resource, "/")

				url, _ := urlpkg.Parse(strings.Join([]string{"https://graph.microsoft.com", version, resource}, "/"))
				query := url.Query()

				if parameters.Query != nil {
					if parameters.Query.Filter != nil {
						query.Set("$filter", *parameters.Query.Filter)
					}

					if parameters.Query.Expand != nil {
						query.Set("$expand", *parameters.Query.Expand)
					}

					if parameters.Query.Select != nil {
						query.Set("$select", *parameters.Query.Select)
					}

					if parameters.Query.OrderBy != nil {
						query.Set("$orderby", *parameters.Query.OrderBy)
					}

					if parameters.Query.Top != nil {
						query.Set("$top", fmt.Sprintf("%d", *parameters.Query.Top))
					}

					if parameters.Query.Skip != nil {
						query.Set("$skip", fmt.Sprintf("%d", *parameters.Query.Skip))
					}
				}

				if len(query) > 0 {
					url.RawQuery = query.Encode()
				}

				var body io.Reader

				if parameters.Body != nil {
					body = strings.NewReader(*parameters.Body)
				}

				token, err := c.Token(ctx)

				if err != nil {
					return nil, err
				}

				r, _ := http.NewRequestWithContext(ctx, strings.ToUpper(parameters.Method), url.String(), body)
				r.Header.Set("Authorization", "Bearer "+token.Token)

				resp, err := c.client.Do(r)

				if err != nil {
					return nil, err
				}

				defer resp.Body.Close()

				result, err := io.ReadAll(resp.Body)

				if err != nil {
					return nil, err
				}

				printResult(r, result)

				return string(result), nil
			},
		},
	}, nil
}

func printResult(r *http.Request, data []byte) {
	cli.Debug(r.Method, r.URL.String())

	var v map[string]any

	if err := json.Unmarshal(data, &v); err == nil {
		data, _ := json.MarshalIndent(v, "  ", "  ")

		cli.Debug(string(data))
		return
	}

	cli.Debug(string(data))
}
