package dap

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/tooling"
)

// AdapterRequirement describes the commands capable of serving one detected
// debugger integration, in preference order.
type AdapterRequirement struct {
	Name     string
	Language string
	Commands []string
	Projects []string
}

// DetectRequirements reports adapter needs independently of whether the
// adapter executable is installed.
func DetectRequirements(ctx context.Context, workspace string, adapters []AdapterDescriptor) ([]AdapterRequirement, error) {
	specs := make([]tooling.ProjectSpec, len(adapters))
	for index, adapter := range adapters {
		specs[index] = tooling.ProjectSpec{Markers: adapter.Markers, Extensions: adapter.SourceExtensions}
	}
	projectsByAdapter, err := tooling.DetectProjects(ctx, workspace, specs)
	if err != nil {
		return nil, err
	}
	var requirements []AdapterRequirement
	for index, adapter := range adapters {
		projects := projectsByAdapter[index]
		if len(projects) == 0 || adapter.Command == "" {
			continue
		}
		requirements = append(requirements, AdapterRequirement{
			Name: adapter.Name, Language: adapter.Language,
			Commands: []string{adapter.Command}, Projects: projects,
		})
	}
	return requirements, nil
}

func MissingAdapterError(requirements []AdapterRequirement) error {
	if len(requirements) == 0 {
		return fmt.Errorf("no debug adapter project was detected in this workspace")
	}
	details := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		command := strings.Join(requirement.Commands, " or ")
		if requirement.Language != "" {
			command += " (" + requirement.Language + ")"
		}
		details = append(details, command)
	}
	return fmt.Errorf("debugging needs %s, but no usable adapter is installed", strings.Join(details, ", "))
}

func detectProjects(ctx context.Context, workspace string, markers, sourceExtensions []string) ([]string, error) {
	return tooling.DetectProject(ctx, workspace, tooling.ProjectSpec{Markers: markers, Extensions: sourceExtensions})
}
