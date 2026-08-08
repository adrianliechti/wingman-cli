package claw

import (
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (t *TUI) refreshTasks() {
	th := theme.Default
	name := t.selected()
	agentDir := t.claw.AgentDir(name)
	tasks, _ := schedule.List(agentDir)

	t.taskLines = nil
	now := time.Now()

	if len(tasks) == 0 {
		t.taskLines = append(t.taskLines, indent+dim("no tasks"))
		return
	}

	for _, task := range tasks {
		icon := ansi.Fg(th.Green) + "●" + ansi.Reset
		if task.Failures > 0 {
			icon = ansi.Fg(th.Red) + "●" + ansi.Reset
		}
		if task.Status == "paused" {
			icon = ansi.Fg(th.BrBlack) + "○" + ansi.Reset
		}

		failStr := ""
		if task.Failures > 0 {
			failStr = " " + ansi.Fg(th.Red) + fmt.Sprintf("failing x%d", task.Failures) + ansi.Reset
		}

		next := schedule.NextRun(task, now)
		nextStr := ""

		if !next.IsZero() {
			dur := next.Sub(now)
			if dur < 0 {
				nextStr = " " + ansi.Fg(th.Red) + "overdue" + ansi.Reset
			} else if dur < time.Hour {
				nextStr = " " + ansi.Fg(th.Green) + fmt.Sprintf("%dm", int(dur.Minutes())+1) + ansi.Reset
			} else {
				nextStr = " " + dim(next.Format("15:04"))
			}
		}

		prompt := strings.ReplaceAll(markdown.Sanitize(task.Prompt), "\n", " ")
		prompt = ansi.Truncate(prompt, 80, "...")

		t.taskLines = append(t.taskLines, fmt.Sprintf("%s%s %s%s%s  %s",
			indent, icon, humanSchedule(task.Schedule), nextStr, failStr, dim(prompt)))
	}
}

func humanSchedule(sched string) string {
	if after, ok := strings.CutPrefix(sched, "every "); ok {
		d, err := time.ParseDuration(after)
		if err != nil {
			return sched
		}

		if d < time.Minute {
			return fmt.Sprintf("every %ds", int(d.Seconds()))
		}

		if d < time.Hour {
			return fmt.Sprintf("every %d min", int(d.Minutes()))
		}

		if d == time.Hour {
			return "every hour"
		}

		if d%time.Hour == 0 {
			h := int(d.Hours())
			if h == 24 {
				return "daily"
			}

			return fmt.Sprintf("every %dh", h)
		}

		return fmt.Sprintf("every %s", d)
	}

	if ts, ok := schedule.OnceTime(sched); ok {
		ts = ts.Local()
		now := time.Now()

		if ts.Year() == now.Year() && ts.YearDay() == now.YearDay() {
			return "today " + ts.Format("15:04")
		}

		tomorrow := now.AddDate(0, 0, 1)

		if ts.Year() == tomorrow.Year() && ts.YearDay() == tomorrow.YearDay() {
			return "tomorrow " + ts.Format("15:04")
		}

		return ts.Format("Jan 2, 15:04")
	}

	fields := strings.Fields(sched)

	if len(fields) >= 5 {
		min, hour, dom, _, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

		if dom == "*" && dow == "*" && min != "*" && hour != "*" {
			return fmt.Sprintf("daily at %s:%s", zeroPad(hour), zeroPad(min))
		}

		if dom == "*" && dow == "1-5" && min != "*" && hour != "*" {
			return fmt.Sprintf("weekdays at %s:%s", zeroPad(hour), zeroPad(min))
		}

		dayNames := map[string]string{"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat", "7": "Sun"}

		if dom == "*" && min != "*" && hour != "*" {
			if name, ok := dayNames[dow]; ok {
				return fmt.Sprintf("%s at %s:%s", name, zeroPad(hour), zeroPad(min))
			}
		}

		if strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && dow == "*" {
			return fmt.Sprintf("every %s min", strings.TrimPrefix(min, "*/"))
		}
	}

	return sched
}

func zeroPad(s string) string {
	if len(s) == 1 {
		return "0" + s
	}

	return s
}
