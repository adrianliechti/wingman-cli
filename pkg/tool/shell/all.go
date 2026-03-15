package shell

import (
	"github.com/adrianliechti/wingman-agent/pkg/tool"
)

func Tools() []tool.Tool {
	return []tool.Tool{
		ShellTool(),
	}
}
