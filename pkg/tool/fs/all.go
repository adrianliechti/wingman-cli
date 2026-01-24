package fs

import (
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func Tools() []tool.Tool {
	return []tool.Tool{
		ReadTool(),
		WriteTool(),
		EditTool(),
		LsTool(),
		FindTool(),
		GrepTool(),
	}
}