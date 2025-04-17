package rest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Confirm func(method, path, contentType string, body io.Reader) error

type Client struct {
	URL string

	bearer   string
	username string
	password string

	confirm Confirm
}

type Option func(*Client)

func New(baseURL string, options ...Option) (*Client, error) {
	url, err := url.Parse(baseURL)

	if err != nil {
		return nil, err
	}

	if !url.IsAbs() || url.Host == "" {
		return nil, fmt.Errorf("invalid base URL")
	}

	c := &Client{
		URL: url.String(),
	}

	for _, o := range options {
		o(c)
	}

	return c, nil
}

func WithBearer(bearer string) func(*Client) {
	return func(c *Client) {
		c.bearer = bearer
	}
}

func WithBasicAuth(username, password string) func(*Client) {
	return func(c *Client) {
		c.username = username
		c.password = password
	}
}

func WithConfirm(confirm Confirm) func(*Client) {
	return func(c *Client) {
		c.confirm = confirm
	}
}

func (c *Client) Execute(ctx context.Context, method, path, contentType string, body io.Reader) ([]byte, error) {
	url := strings.TrimRight(c.URL, "/") + "/" + strings.TrimLeft(path, "/")

	var err error
	var data []byte

	if body != nil {
		data, err = io.ReadAll(body)

		if err != nil {
			return nil, err
		}

		body = bytes.NewReader(data)
	}

	if c.confirm != nil {
		if err := c.confirm(method, path, contentType, body); err != nil {
			return nil, err
		}

		if data != nil {
			body = bytes.NewReader(data)
		}
	}

	req, _ := http.NewRequestWithContext(ctx, method, url, body)
	req.Header.Set("Content-Type", contentType)

	if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	result, err := io.ReadAll(resp.Body)

	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		status := fmt.Sprintf("%d %s", resp.StatusCode, resp.Status)
		return []byte(status), nil
	}

	return result, nil
}
