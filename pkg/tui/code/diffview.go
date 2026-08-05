package code

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/rewind"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (a *App) showDiffView() {
	t := theme.Default

	diffs, err := a.agent.Workspace().Diffs()
	if err != nil {
		a.appendChat(cellNotice(fmt.Sprintf("%v", err), t.Yellow, a.width()))
		return
	}

	if len(diffs) == 0 {
		a.showToast("No changes from the session baseline", t.BrBlack)
		return
	}

	var totalInsertions, totalDeletions int
	for _, diff := range diffs {
		ins, del := countDiffStats(diff.Patch)
		totalInsertions += ins
		totalDeletions += del
	}

	status := dim(fmt.Sprintf("%d file(s)", len(diffs))) + " " +
		colored(t.Green, fmt.Sprintf("+%d", totalInsertions)) + " " +
		colored(t.Red, fmt.Sprintf("-%d", totalDeletions))

	item := func(selected bool, i int) string {
		diff := diffs[i]

		var statusColor = t.Foreground
		icon := "●"

		switch diff.Status {
		case rewind.StatusAdded:
			statusColor = t.Green
		case rewind.StatusModified:
			statusColor = t.Yellow
		case rewind.StatusDeleted:
			statusColor = t.Red
		default:
			icon = "○"
		}

		ins, del := countDiffStats(diff.Patch)
		stats := colored(t.Green, fmt.Sprintf("+%d", ins)) + " " + colored(t.Red, fmt.Sprintf("-%d", del))

		if selected {
			return colored(t.Cyan, "→ ") + colored(statusColor, icon) + " " + colored(t.Cyan, diff.Path) + " " + stats
		}
		return "  " + colored(statusColor, icon) + " " + diff.Path + " " + stats
	}

	content := func(i int) []string {
		return strings.Split(markdown.HighlightDiff(diffs[i].Patch), "\n")
	}
	search := func(i int) string {
		return diffs[i].Path + "\n" + diffs[i].Patch
	}

	a.openOverlay(newTwoPaneOverlay("changes", status, len(diffs), item, content, search))
}

func countDiffStats(patch string) (insertions, deletions int) {
	for line := range strings.SplitSeq(patch, "\n") {
		if len(line) == 0 {
			continue
		}
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") {
			continue
		}

		switch line[0] {
		case '+':
			insertions++
		case '-':
			deletions++
		}
	}

	return
}
