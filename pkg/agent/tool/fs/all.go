package fs

import (
	"os"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type Options struct {
	AllowedReadRoots  []string
	AllowedWriteRoots []string
	Freshness         *Freshness

	// MaxReadFileBytes switches larger files to bounded streaming reads.
	// Zero uses MaxReadFileBytes; a negative value loads files without a size limit.
	MaxReadFileBytes int64
}

func Tools(root *os.Root, opts *Options) []tool.Tool {
	if opts == nil {
		opts = &Options{}
	}
	tracker := newContentTracker()
	return []tool.Tool{
		readTool(root, tracker, opts.Freshness, opts.MaxReadFileBytes, opts.AllowedReadRoots...),
		writeTool(root, tracker, opts.Freshness, opts.AllowedWriteRoots...),
		editTool(root, tracker, opts.Freshness, opts.AllowedWriteRoots...),
		GrepTool(root, opts.AllowedReadRoots...),
		GlobTool(root, opts.AllowedReadRoots...),
		ImageTool(root, opts.AllowedReadRoots...),
		ApplyPatchTool(root, opts.Freshness),
	}
}
