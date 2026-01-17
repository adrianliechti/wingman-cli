package shell

import (
	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func Tools() []tool.Tool {
	return []tool.Tool{
		ShellTool(),
	}
}
