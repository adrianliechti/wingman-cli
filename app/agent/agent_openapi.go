package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/rest"
	"github.com/adrianliechti/wingman-cli/pkg/tool/openapi"
	"github.com/adrianliechti/wingman-cli/pkg/util"

	"github.com/adrianliechti/go-cli"
	wingman "github.com/adrianliechti/wingman/pkg/client"
)

var (
	//go:embed prompt_openapi.txt
	prompt_openapi string
)

func RunOpenAPI(ctx context.Context, client *wingman.Client, model string, path, url, bearer, username, password string) error {
	cli.Info("🤗 Hello, I'm your OpenAPI AI Assistant")
	cli.Info()

	c, err := rest.New(url,
		rest.WithBearer(bearer),
		rest.WithBasicAuth(username, password),
		rest.WithConfirm(handleConfirm),
	)

	if err != nil {
		return err
	}

	catalog, err := openapi.New(path, c)

	if err != nil {
		return err
	}

	tools, err := catalog.Tools(ctx)

	if err != nil {
		return err
	}

	tools = util.OptimizeTools(client, model, tools)

	return Run(ctx, client, model, tools, &RunOptions{
		Prompt: prompt_openapi,
	})
}

func handleConfirm(method, path, contentType string, body io.Reader) error {
	cli.Infof("⚡️ %s %s", method, path)
	cli.Info()

	if body != nil && contentType == "application/json" {
		var val map[string]any

		json.NewDecoder(body).Decode(&val)
		data, _ := json.MarshalIndent(val, "", "  ")

		cli.Debug(string(data))
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
