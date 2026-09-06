package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/adrianliechti/wingman-agent/pkg/mcp"
)

const (
	mcpScopeGlobal  = "global"
	mcpScopeProject = "project"
)

type mcpEntry struct {
	Name   string
	Scope  string
	Path   string
	Server mcp.ServerConfig
}

func runMCP(ctx context.Context, args []string) {
	if len(args) == 0 {
		printMCPHelp(os.Stdout)
		return
	}

	var err error

	switch args[0] {
	case "--help", "-h", "help":
		printMCPHelp(os.Stdout)
	case "list", "ls":
		err = runMCPList(args[1:])
	case "get":
		err = runMCPGet(args[1:])
	case "add":
		err = runMCPAdd(ctx, args[1:])
	case "remove", "rm":
		err = runMCPRemove(args[1:])
	case "login":
		err = runMCPLogin(ctx, args[1:])
	case "logout":
		err = runMCPLogout(args[1:])
	default:
		err = fmt.Errorf("unknown mcp command %q (run 'wingman mcp --help' for usage)", args[0])
	}

	if err != nil {
		fatal(err)
	}
}

func mcpConfigPath(scope string) (string, error) {
	switch scope {
	case mcpScopeGlobal:
		path := mcp.GlobalConfigPath()

		if path == "" {
			return "", errors.New("cannot resolve the global mcp.json path")
		}

		return path, nil
	case mcpScopeProject:
		wd, err := os.Getwd()

		if err != nil {
			return "", err
		}

		return filepath.Join(wd, "mcp.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q (use global or project)", scope)
	}
}

// loadMCPEntries merges both configs the way the agent does: project
// definitions shadow global ones of the same name.
func loadMCPEntries() ([]mcpEntry, error) {
	byName := map[string]mcpEntry{}

	for _, scope := range []string{mcpScopeGlobal, mcpScopeProject} {
		path, err := mcpConfigPath(scope)

		if err != nil {
			return nil, err
		}

		cfg, err := mcp.LoadConfig(path)

		if errors.Is(err, os.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		for name, server := range cfg.Servers {
			byName[name] = mcpEntry{Name: name, Scope: scope, Path: path, Server: server}
		}
	}

	entries := make([]mcpEntry, 0, len(byName))

	for _, name := range slices.Sorted(maps.Keys(byName)) {
		entries = append(entries, byName[name])
	}

	return entries, nil
}

func findMCPEntry(name string) (mcpEntry, error) {
	entries, err := loadMCPEntries()

	if err != nil {
		return mcpEntry{}, err
	}

	for _, entry := range entries {
		if entry.Name == name {
			return entry, nil
		}
	}

	return mcpEntry{}, fmt.Errorf("MCP server %q not found (run 'wingman mcp list')", name)
}

func mcpTransportName(server mcp.ServerConfig) string {
	if server.URL == "" {
		return "stdio"
	}

	if server.Transport == "sse" {
		return "sse"
	}

	return "http"
}

func mcpTarget(server mcp.ServerConfig) string {
	if server.URL != "" {
		return server.URL
	}

	return strings.Join(append([]string{server.Command}, server.Args...), " ")
}

func mcpAuthStatus(store *mcp.CredentialStore, server mcp.ServerConfig) string {
	if server.URL == "" {
		return "-"
	}

	for name := range server.Headers {
		if strings.EqualFold(name, "Authorization") {
			return "header"
		}
	}

	cred, err := store.Get(server.URL)

	if err != nil || cred == nil || cred.Token == nil {
		return "not logged in"
	}

	if cred.Token.Valid() || cred.Token.RefreshToken != "" {
		return "logged in"
	}

	return "expired"
}

func runMCPList(args []string) error {
	fs := newFlags("wingman mcp list")

	if err := fs.Parse(args); err != nil {
		return err
	}

	entries, err := loadMCPEntries()

	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Println("No MCP servers configured (run 'wingman mcp add --help')")
		return nil
	}

	store := mcp.DefaultCredentialStore()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tTARGET\tSCOPE\tAUTH")

	for _, entry := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", entry.Name, mcpTransportName(entry.Server), mcpTarget(entry.Server), entry.Scope, mcpAuthStatus(store, entry.Server))
	}

	return w.Flush()
}

func runMCPGet(args []string) error {
	fs := newFlags("wingman mcp get NAME")

	positional, err := fs.ParseArgs(args)

	if err != nil {
		return err
	}

	if len(positional) != 1 {
		return errors.New("usage: wingman mcp get NAME")
	}

	entry, err := findMCPEntry(positional[0])

	if err != nil {
		return err
	}

	printMCPEntry(os.Stdout, entry, mcp.DefaultCredentialStore())

	return nil
}

func printMCPEntry(w io.Writer, entry mcpEntry, store *mcp.CredentialStore) {
	server := entry.Server

	fmt.Fprintf(w, "%s\n", entry.Name)
	fmt.Fprintf(w, "  Scope:     %s (%s)\n", entry.Scope, entry.Path)
	fmt.Fprintf(w, "  Type:      %s\n", mcpTransportName(server))

	if server.URL != "" {
		fmt.Fprintf(w, "  URL:       %s\n", server.URL)

		for _, name := range slices.Sorted(maps.Keys(server.Headers)) {
			fmt.Fprintf(w, "  Header:    %s: %s\n", name, server.Headers[name])
		}

		if server.OAuth != nil && server.OAuth.ClientID != "" {
			fmt.Fprintf(w, "  Client ID: %s\n", server.OAuth.ClientID)
		}

		if server.OAuth != nil && server.OAuth.CallbackPort > 0 {
			fmt.Fprintf(w, "  Callback:  port %d\n", server.OAuth.CallbackPort)
		}

		fmt.Fprintf(w, "  Auth:      %s\n", mcpAuthStatus(store, server))
	} else {
		fmt.Fprintf(w, "  Command:   %s\n", mcpTarget(server))

		for _, name := range slices.Sorted(maps.Keys(server.Env)) {
			fmt.Fprintf(w, "  Env:       %s=%s\n", name, server.Env[name])
		}
	}
}

type mcpAddOptions struct {
	Scope   string
	NoLogin bool
}

func parseMCPAdd(args []string) (string, mcp.ServerConfig, mcpAddOptions, error) {
	var (
		url          string
		transport    string
		headers      []string
		env          []string
		clientID     string
		callbackPort int
	)

	opts := mcpAddOptions{Scope: mcpScopeGlobal}

	fs := newFlags("wingman mcp add NAME (--url URL | -- COMMAND [ARGS...])")
	fs.String(&url, "--url URL", "endpoint of a remote (HTTP) server")
	fs.String(&transport, "--transport, -t TYPE", "http (default) or sse for remote servers")
	fs.Strings(&headers, "--header, -H \"K: V\"", "header sent to a remote server (repeatable)")
	fs.Strings(&env, "--env, -e K=V", "environment variable for a stdio server (repeatable)")
	fs.String(&clientID, "--client-id ID", "pre-registered OAuth client ID")
	fs.Int(&callbackPort, "--callback-port N", fmt.Sprintf("fixed port for the OAuth callback (default: %d)", mcp.DefaultCallbackPort))
	fs.String(&opts.Scope, "--scope, -s SCOPE", "global (~/.wingman/mcp.json, default) or project (./mcp.json)")
	fs.Bool(&opts.NoLogin, "--no-login", "do not connect or log in after adding")

	positional, err := fs.ParseArgs(args)

	if err != nil {
		return "", mcp.ServerConfig{}, opts, err
	}

	if len(positional) == 0 {
		return "", mcp.ServerConfig{}, opts, errors.New("usage: wingman mcp add NAME (--url URL | -- COMMAND [ARGS...])")
	}

	name := positional[0]
	command := positional[1:]

	if name == "" || strings.ContainsAny(name, " /\\") {
		return "", mcp.ServerConfig{}, opts, fmt.Errorf("invalid server name %q", name)
	}

	if _, err := mcpConfigPath(opts.Scope); err != nil {
		return "", mcp.ServerConfig{}, opts, err
	}

	var server mcp.ServerConfig

	switch {
	case url != "" && len(command) > 0:
		return "", mcp.ServerConfig{}, opts, errors.New("specify either --url or a command, not both")

	case url != "":
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return "", mcp.ServerConfig{}, opts, fmt.Errorf("invalid url %q", url)
		}

		if len(env) > 0 {
			return "", mcp.ServerConfig{}, opts, errors.New("--env only applies to stdio servers")
		}

		server.URL = url

		switch transport {
		case "", "http", "streamable-http", "streamable_http":
		case "sse":
			server.Transport = "sse"
		default:
			return "", mcp.ServerConfig{}, opts, fmt.Errorf("unknown transport %q (use http or sse)", transport)
		}

		for _, header := range headers {
			key, value, ok := strings.Cut(header, ":")

			if !ok || strings.TrimSpace(key) == "" {
				return "", mcp.ServerConfig{}, opts, fmt.Errorf("invalid header %q (expected \"Name: value\")", header)
			}

			if server.Headers == nil {
				server.Headers = map[string]string{}
			}

			server.Headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}

		if clientID != "" || callbackPort > 0 {
			server.OAuth = &mcp.OAuthConfig{ClientID: clientID, CallbackPort: callbackPort}
		}

	case len(command) > 0:
		if transport != "" && transport != "stdio" {
			return "", mcp.ServerConfig{}, opts, fmt.Errorf("transport %q requires --url", transport)
		}

		if len(headers) > 0 || clientID != "" || callbackPort > 0 {
			return "", mcp.ServerConfig{}, opts, errors.New("--header, --client-id and --callback-port only apply to remote servers")
		}

		server.Command = command[0]
		server.Args = command[1:]

		for _, pair := range env {
			key, value, ok := strings.Cut(pair, "=")

			if !ok || key == "" {
				return "", mcp.ServerConfig{}, opts, fmt.Errorf("invalid env %q (expected KEY=value)", pair)
			}

			if server.Env == nil {
				server.Env = map[string]string{}
			}

			server.Env[key] = value
		}

	default:
		return "", mcp.ServerConfig{}, opts, errors.New("specify --url URL or a command after --")
	}

	return name, server, opts, nil
}

func runMCPAdd(ctx context.Context, args []string) error {
	name, server, opts, err := parseMCPAdd(args)

	if err != nil {
		return err
	}

	path, err := mcpConfigPath(opts.Scope)

	if err != nil {
		return err
	}

	if err := mcp.SaveServer(path, name, server); err != nil {
		return err
	}

	fmt.Printf("Added MCP server %q to %s\n", name, path)

	if server.URL == "" || opts.NoLogin {
		return nil
	}

	if err := loginMCP(ctx, name, server, false, 0); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not connect to %q: %v\n", name, err)
	}

	return nil
}

func runMCPRemove(args []string) error {
	var scope string

	fs := newFlags("wingman mcp remove NAME")
	fs.String(&scope, "--scope, -s SCOPE", "only remove from global or project config")

	positional, err := fs.ParseArgs(args)

	if err != nil {
		return err
	}

	if len(positional) != 1 {
		return errors.New("usage: wingman mcp remove NAME")
	}

	name := positional[0]
	scopes := []string{mcpScopeProject, mcpScopeGlobal}

	if scope != "" {
		scopes = []string{scope}
	}

	var removed bool

	for _, scope := range scopes {
		path, err := mcpConfigPath(scope)

		if err != nil {
			return err
		}

		ok, err := mcp.RemoveServer(path, name)

		if err != nil {
			return err
		}

		if ok {
			removed = true
			fmt.Printf("Removed MCP server %q from %s\n", name, path)
		}
	}

	if !removed {
		return fmt.Errorf("MCP server %q not found", name)
	}

	return nil
}

func runMCPLogin(ctx context.Context, args []string) error {
	var callbackPort int

	fs := newFlags("wingman mcp login NAME")
	fs.Int(&callbackPort, "--callback-port N", "port for the OAuth callback (overrides the server config)")

	positional, err := fs.ParseArgs(args)

	if err != nil {
		return err
	}

	if len(positional) != 1 {
		return errors.New("usage: wingman mcp login NAME")
	}

	entry, err := findMCPEntry(positional[0])

	if err != nil {
		return err
	}

	return loginMCP(ctx, entry.Name, entry.Server, true, callbackPort)
}

func loginMCP(ctx context.Context, name string, server mcp.ServerConfig, reauthenticate bool, callbackPort int) error {
	result, err := mcp.Login(ctx, server, mcp.LoginOptions{
		CallbackPort:   callbackPort,
		Reauthenticate: reauthenticate,
		Output:         os.Stderr,
	})

	if err != nil {
		return err
	}

	defer result.Session.Close()

	tools, err := result.Session.ListTools(ctx, nil)

	if err != nil {
		return err
	}

	switch {
	case result.Prompted:
		fmt.Printf("Logged in to %q (%d tools available)\n", name, len(tools.Tools))
	default:
		fmt.Printf("Connected to %q without login (%d tools available)\n", name, len(tools.Tools))
	}

	return nil
}

func runMCPLogout(args []string) error {
	fs := newFlags("wingman mcp logout NAME")

	positional, err := fs.ParseArgs(args)

	if err != nil {
		return err
	}

	if len(positional) != 1 {
		return errors.New("usage: wingman mcp logout NAME")
	}

	entry, err := findMCPEntry(positional[0])

	if err != nil {
		return err
	}

	if entry.Server.URL == "" {
		return fmt.Errorf("MCP server %q is not a remote server", entry.Name)
	}

	removed, err := mcp.DefaultCredentialStore().Delete(entry.Server.URL)

	if err != nil {
		return err
	}

	if !removed {
		fmt.Printf("No stored credentials for %q\n", entry.Name)
		return nil
	}

	fmt.Printf("Logged out of %q\n", entry.Name)

	return nil
}

func printMCPHelp(w io.Writer) {
	fmt.Fprintf(w, `wingman mcp — manage MCP servers

Usage:
  wingman mcp list                               List configured servers (alias: ls)
  wingman mcp get NAME                           Show a server's configuration
  wingman mcp add NAME --url URL [flags]         Add a remote (HTTP/SSE) server
  wingman mcp add NAME [flags] -- COMMAND [ARGS] Add a stdio server
  wingman mcp remove NAME [--scope SCOPE]        Remove a server (alias: rm)
  wingman mcp login NAME [--callback-port N]     Log in to a remote server with OAuth
  wingman mcp logout NAME                        Forget stored OAuth credentials

Add flags:
  --url URL             Endpoint of a remote server
  --transport, -t TYPE  http (default) or sse for remote servers
  --header, -H "K: V"   Header sent to a remote server (repeatable)
  --env, -e K=V         Environment variable for a stdio server (repeatable)
  --client-id ID        Pre-registered OAuth client ID (default: dynamic registration)
  --callback-port N     Fixed port for the OAuth callback (default: %d)
  --scope, -s SCOPE     global (~/.wingman/mcp.json, default) or project (./mcp.json)
  --no-login            Do not connect or log in after adding

Remote servers that require OAuth open your browser on 'add' and 'login'.
Tokens are stored in ~/.wingman/mcp-credentials.json and refreshed automatically.

Examples:
  wingman mcp add remote --url https://mcp.example.com/mcp
  wingman mcp add fs -- npx -y @modelcontextprotocol/server-filesystem .
  wingman mcp add api --url https://api.example.com/mcp -H "X-Api-Key: secret"
  wingman mcp login remote
`, mcp.DefaultCallbackPort)
}
