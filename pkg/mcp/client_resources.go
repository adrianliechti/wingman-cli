package mcp

import (
	"context"
	"errors"

	"github.com/adrianliechti/wingman-cli/pkg/resource"

	"mcp"
)

func (m *Manager) Resources(ctx context.Context) ([]resource.Resource, error) {
	var result []resource.Resource

	for _, c := range m.clients {
		s, err := c.connect(ctx)

		if err != nil {
			return nil, err
		}

		defer s.Close()

		resp, err := s.ListResources(ctx, &mcp.ListResourcesParams{})

		if err != nil {
			return nil, err
		}

		for _, r := range resp.Resources {
			resource := resource.Resource{
				URI: r.URI,

				Name:        r.Name,
				Description: r.Description,

				ContentType: r.MIMEType,

				Content: func(ctx context.Context) ([]byte, error) {
					s, err := c.connect(ctx)

					if err != nil {
						return nil, err
					}

					defer s.Close()

					result, err := s.ReadResource(ctx, &mcp.ReadResourceParams{
						URI: r.URI,
					})

					if err != nil {
						return nil, err
					}

					if result.Contents == nil {
						return nil, errors.New("no content returned")
					}

					if result.Contents.Blob != nil {
						return []byte(result.Contents.Blob), nil
					}

					if result.Contents.Text != "" {
						return []byte(result.Contents.Text), nil
					}

					return nil, errors.New("no content returned")
				},
			}

			result = append(result, resource)
		}
	}

	return result, nil
}
