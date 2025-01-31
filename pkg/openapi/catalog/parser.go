package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/invopop/yaml"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
)

func parse(path string) (*openapi3.T, error) {
	if strings.Contains(path, "http://") || strings.Contains(path, "https://") {
		dir, err := os.MkdirTemp("", "openapi-*")

		if err != nil {
			return nil, err
		}

		resp, err := http.DefaultClient.Get(path)

		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)

		if err != nil {
			return nil, err
		}

		path = filepath.Join(dir, "spec")

		if err := os.WriteFile(path, data, 0644); err != nil {
			return nil, err
		}

		defer os.RemoveAll(dir)
	}

	if doc, err := parseV3(path); err == nil {
		doc.InternalizeRefs(context.Background(), nil)
		return doc, nil
	}

	if doc, err := parseV2(path); err == nil {
		doc.InternalizeRefs(context.Background(), nil)
		return doc, nil
	}

	return nil, errors.New("failed to parse OpenAPI document")
}

func parseV3(path string) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	return loader.LoadFromFile(path)
}

func parseV2(path string) (*openapi3.T, error) {
	input, err := os.ReadFile(path)

	if err != nil {
		return nil, err
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	doc := new(openapi2.T)

	if err = json.Unmarshal(input, &doc); err == nil {
		return openapi2conv.ToV3WithLoader(doc, loader, nil)
	}

	if err = yaml.Unmarshal(input, &doc); err == nil {
		return openapi2conv.ToV3WithLoader(doc, loader, nil)
	}

	return nil, errors.New("failed to parse / convert OpenAPI v2 document")
}
