package main

import (
	"flag"
	"log"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/adrianliechti/wingman-cli/pkg/server"
	"github.com/adrianliechti/wingman-cli/pkg/tool"
	"github.com/adrianliechti/wingman-cli/pkg/tool/fs"
	"github.com/adrianliechti/wingman-cli/pkg/tool/plan"
	"github.com/adrianliechti/wingman-cli/pkg/tool/shell"
)

func main() {
	addr := flag.String("addr", ":3000", "address to listen on")
	flag.Parse()

	wd, err := os.Getwd()

	if err != nil {
		log.Fatalf("failed to get working directory: %v", err)
	}

	root, err := os.OpenRoot(wd)

	if err != nil {
		log.Fatalf("failed to open root: %v", err)
	}

	defer root.Close()

	scratchDir, err := os.MkdirTemp("", "wingman-server-*")

	if err != nil {
		log.Fatalf("failed to create scratch directory: %v", err)
	}

	defer os.RemoveAll(scratchDir)

	scratch, err := os.OpenRoot(scratchDir)

	if err != nil {
		log.Fatalf("failed to open scratch: %v", err)
	}

	defer scratch.Close()

	env := &tool.Environment{
		Date: time.Now().Format("January 2, 2006"),

		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,

		Root:    root,
		Scratch: scratch,
	}

	tools := slices.Concat(fs.Tools(), shell.Tools(), plan.Tools())

	s := server.New(tools, env)

	log.Printf("Starting MCP server on %s", *addr)
	log.Printf("Working directory: %s", wd)

	if err := s.ListenAndServe(*addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
