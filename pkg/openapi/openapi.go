package openapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adrianliechti/wingman/pkg/cli"
	"github.com/adrianliechti/wingman/pkg/openapi/catalog"
	"github.com/adrianliechti/wingman/pkg/openapi/client"

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

	stdin := bufio.NewReader(os.Stdin)

	for {
		println("")
		println("")
		print(">>> ")

		input, err := stdin.ReadString('\n')

		if err != nil {
			panic(err)
		}

		input = strings.TrimRight(input, " \n")

		result, err := catalog.Query(ctx, model, input)

		if err != nil {
			panic(err)
		}

		println(result)
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
