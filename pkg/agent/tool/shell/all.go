package shell

import (
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func Tools() []tool.Tool {
	return []tool.Tool{
		ShellTool(),
	}
}
