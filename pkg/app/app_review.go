package app

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/agent"
	"github.com/adrianliechti/wingman-cli/pkg/prompt"
	"github.com/adrianliechti/wingman-cli/pkg/rewind"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

// reviewData holds template data for the review prompt
type reviewData struct {
	Date       string
	OS         string
	Arch       string
	WorkingDir string
	Diff       string
}

// startReview initiates a code review with the AI agent
func (a *App) startReview(commitRef string) {
	t := theme.Default

	// Wait for rewind to be ready
	select {
	case <-a.rewindReady:
	default:
		fmt.Fprintf(a.chatView, "[%s]Review not available (initializing...)[-]\n\n", t.Yellow)
		return
	}

	if a.rewind == nil {
		fmt.Fprintf(a.chatView, "[%s]Review not available[-]\n\n", t.Yellow)
		return
	}

	diffs, err := a.rewind.DiffFromBaseline()
	if err != nil {
		fmt.Fprintf(a.chatView, "[%s]%v[-]\n\n", t.Yellow, err)
		return
	}

	// Build diff content for the prompt
	var diffContent strings.Builder
	for _, diff := range diffs {
		var statusStr string
		switch diff.Status {
		case rewind.StatusAdded:
			statusStr = "added"
		case rewind.StatusModified:
			statusStr = "modified"
		case rewind.StatusDeleted:
			statusStr = "deleted"
		}
		diffContent.WriteString(fmt.Sprintf("\n### File: %s (%s)\n", diff.Path, statusStr))
		diffContent.WriteString("```diff\n")
		diffContent.WriteString(diff.Patch)
		diffContent.WriteString("```\n")
	}

	// Prepare review data
	env := a.config.Environment
	data := reviewData{
		Date:       env.Date,
		OS:         env.OS,
		Arch:       env.Arch,
		WorkingDir: env.WorkingDir(),
		Diff:       diffContent.String(),
	}

	// Render the review prompt
	reviewPrompt, err := prompt.Render(prompt.Review, data)
	if err != nil {
		fmt.Fprintf(a.chatView, "[%s]Failed to render review prompt: %v[-]\n\n", t.Yellow, err)
		return
	}

	// Display what we're reviewing
	a.switchToChat()
	fmt.Fprintf(a.chatView, "[%s::b]Starting code review against baseline...[-::-]\n\n", t.Cyan)

	// Send to agent for review
	input := []agent.Content{{Text: "Please review the code changes."}}

	go a.streamResponse(input, reviewPrompt, a.allTools())
}
