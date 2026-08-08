package mcp

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Dedup removes servers that are configured identically to one already kept, so
// the same endpoint is not connected twice and its tools are not offered under
// two names. Only a complete match collapses: one differing header or
// environment variable is a deliberate distinction and keeps both alive.
//
// preferred lists names in precedence order and decides which name survives;
// any remaining name is considered afterwards in sorted order.
func Dedup(servers map[string]ServerConfig, preferred []string) []string {
	if len(servers) < 2 {
		return nil
	}

	order := make([]string, 0, len(servers))
	queued := make(map[string]bool, len(servers))

	for _, name := range preferred {
		if _, ok := servers[name]; ok && !queued[name] {
			queued[name] = true
			order = append(order, name)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(servers)) {
		if !queued[name] {
			order = append(order, name)
		}
	}

	var dropped []string

	kept := make(map[string]string, len(servers))

	for _, name := range order {
		key := identity(servers[name])

		if winner, ok := kept[key]; ok {
			delete(servers, name)
			dropped = append(dropped, fmt.Sprintf("MCP server %q duplicates %q; using %q", name, winner, winner))
			continue
		}

		kept[key] = name
	}

	return dropped
}

func identity(server ServerConfig) string {
	var sb strings.Builder

	if server.Command != "" {
		fmt.Fprintf(&sb, "stdio\x00%s\x00%s\x00", server.Command, server.Dir)

		for _, arg := range server.Args {
			fmt.Fprintf(&sb, "%s\x01", arg)
		}

		sb.WriteByte(0)
		writePairs(&sb, server.Env)

		return sb.String()
	}

	transport := server.Transport
	if transport == "" {
		transport = "streamable-http"
	}

	fmt.Fprintf(&sb, "%s\x00%s\x00", transport, server.URL)
	writePairs(&sb, server.Headers)

	return sb.String()
}

func writePairs(sb *strings.Builder, values map[string]string) {
	for _, key := range slices.Sorted(maps.Keys(values)) {
		fmt.Fprintf(sb, "%s\x01%s\x01", key, values[key])
	}
}
