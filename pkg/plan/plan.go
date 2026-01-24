package plan

import (
	"fmt"
)

type Plan struct {
	Title string `json:"title,omitempty"`
	Tasks []Task `json:"tasks"`
}

type Task struct {
	Description string `json:"description"`
	Status      Status `json:"status"`
}

type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusSkipped Status = "skipped"
)

// IsEmpty returns true if no plan has been set
func (p *Plan) IsEmpty() bool {
	return len(p.Tasks) == 0
}

// Clear resets the plan
func (p *Plan) Clear() {
	p.Title = ""
	p.Tasks = nil
}

// SetTasks creates a new plan with the given title and task descriptions
func (p *Plan) SetTasks(title string, descriptions []string) {
	p.Title = title
	p.Tasks = make([]Task, len(descriptions))

	for i, desc := range descriptions {
		p.Tasks[i] = Task{
			Description: desc,
			Status:      StatusPending,
		}
	}
}

// UpdateTask updates a task status by index
func (p *Plan) UpdateTask(index int, status Status) error {
	if p.IsEmpty() {
		return fmt.Errorf("no plan exists")
	}

	if index < 0 || index >= len(p.Tasks) {
		return fmt.Errorf("task index %d out of range (0-%d)", index, len(p.Tasks)-1)
	}

	p.Tasks[index].Status = status

	return nil
}