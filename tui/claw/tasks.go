package claw

import (
	"fmt"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/claw/tool/schedule"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func (t *TUI) refreshTasks() {
	th := theme.Default
	name := t.selected()
	agentDir := t.claw.AgentDir(name)
	tasks := schedule.LoadTasks(agentDir)

	t.taskView.Clear()
	now := time.Now()

	if len(tasks) == 0 {
		fmt.Fprintf(t.taskView, "  [%s]no tasks[-]", th.BrBlack)
		return
	}

	for _, task := range tasks {
		icon := "[green]\u25cf[-]"
		if task.Status == "paused" {
			icon = fmt.Sprintf("[%s]\u25cb[-]", th.BrBlack)
		}

		next := schedule.NextRun(task, now)
		nextStr := ""

		if !next.IsZero() {
			dur := next.Sub(now)
			if dur < 0 {
				nextStr = fmt.Sprintf(" [%s]overdue[-]", th.Red)
			} else if dur < time.Hour {
				nextStr = fmt.Sprintf(" [%s]%dm[-]", th.Green, int(dur.Minutes())+1)
			} else {
				nextStr = fmt.Sprintf(" [%s]%s[-]", th.BrBlack, next.Format("15:04"))
			}
		}

		prompt := task.Prompt
		if len(prompt) > 80 {
			prompt = prompt[:77] + "..."
		}

		fmt.Fprintf(t.taskView, "  %s [%s]%s[-]%s  [%s]%s[-]\n", icon, th.Foreground, humanSchedule(task.Schedule), nextStr, th.BrBlack, prompt)
	}
}

func humanSchedule(sched string) string {
	// Interval: "every 15m" -> "every 15 min"
	if strings.HasPrefix(sched, "every ") {
		d, err := time.ParseDuration(strings.TrimPrefix(sched, "every "))
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

	// One-time: ISO timestamp -> "Apr 15, 09:00"
	if ts, err := time.Parse(time.RFC3339, sched); err == nil {
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

	// Cron: parse common patterns
	fields := strings.Fields(sched)

	if len(fields) >= 5 {
		min, hour, dom, _, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

		// "0 9 * * *" -> "daily at 09:00"
		if dom == "*" && dow == "*" && min != "*" && hour != "*" {
			return fmt.Sprintf("daily at %s:%s", zeroPad(hour), zeroPad(min))
		}

		// "0 9 * * 1-5" -> "weekdays at 09:00"
		if dom == "*" && dow == "1-5" && min != "*" && hour != "*" {
			return fmt.Sprintf("weekdays at %s:%s", zeroPad(hour), zeroPad(min))
		}

		// "0 9 * * 1" -> "Mon at 09:00"
		dayNames := map[string]string{"0": "Sun", "1": "Mon", "2": "Tue", "3": "Wed", "4": "Thu", "5": "Fri", "6": "Sat", "7": "Sun"}

		if dom == "*" && min != "*" && hour != "*" {
			if name, ok := dayNames[dow]; ok {
				return fmt.Sprintf("%s at %s:%s", name, zeroPad(hour), zeroPad(min))
			}
		}

		// "*/15 * * * *" -> "every 15 min"
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
