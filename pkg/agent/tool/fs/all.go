package fs

import (
	"os"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func Tools(root *os.Root) []tool.Tool {
	return []tool.Tool{
		ReadTool(root),
		WriteTool(root),
		EditTool(root),
		LsTool(root),
		FindTool(root),
		GrepTool(root),
	}
}
