package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adrianliechti/wingman/pkg/cli"
	"github.com/adrianliechti/wingman/pkg/markdown"
	"github.com/adrianliechti/wingman/pkg/openapi/catalog"
	"github.com/adrianliechti/wingman/pkg/openapi/client"

	"github.com/charmbracelet/huh"
	"github.com/openai/openai-go"
)

func Run(ctx context.Context, c *openai.Client, model, path, url, bearer, username, password string) error {
	client, err := client.New(url,
		client.WithBearer(bearer),
		client.WithBasicAuth(username, password),
		client.WithConfirm(handleConfirm),
	)

	if err != nil {
		return err
	}

	catalog, err := catalog.New(path, client, c)

	if err != nil {
		return err
	}

	println()

	for {
		var prompt string

		if err := huh.NewText().
			Lines(2).
			Value(&prompt).
			Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil
			}

			return err
		}

		prompt = strings.TrimSpace(prompt)

		if prompt == "" {
			continue
		}

		println("> " + prompt)
		println()

		result, err := catalog.Query(ctx, model, prompt)

		if err != nil {
			panic(err)
		}

		markdown.Render(os.Stdout, result)
	}
}

func handleConfirm(method, path, contentType string, body io.Reader) error {
	fmt.Printf("⚡️ %s %s", method, path)
	fmt.Println()

	if body != nil && contentType == "application/json" {
		var val map[string]any

		json.NewDecoder(body).Decode(&val)
		data, _ := json.MarshalIndent(val, "", "  ")

		fmt.Println(string(data))
	}

	if strings.EqualFold(method, "HEAD") || strings.EqualFold(method, "GET") {
		return nil
	}

	ok, err := cli.Confirm("Are you sure?", true)

	if err != nil {
		return err
	}

	if !ok {
		return errors.New("operation cancelled by user")
	}

	return nil
}
