package fs

import (
	"github.com/adrianliechti/wingman-agent/pkg/tool"
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
