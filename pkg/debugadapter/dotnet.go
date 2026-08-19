package debugadapter

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/dap"
)

const dotnetLanguage = "C#/.NET"

var (
	csharpMainPattern            = regexp.MustCompile(`(?is)\bstatic\s+(?:async\s+)?(?:void|int|(?:System\s*\.\s*Threading\s*\.\s*Tasks\s*\.\s*)?Task(?:\s*<\s*int\s*>)?)\s+Main\s*\(`)
	csharpTypeDeclarationPattern = regexp.MustCompile(`(?i)^(?:(?:public|internal|private|protected|static|abstract|sealed|partial|file|readonly|ref|unsafe)\s+)*(?:class|struct|interface|record|enum|delegate)\b`)
)

type dotnetAdapter struct{}

func (dotnetAdapter) Language() string { return dotnetLanguage }

func (dotnetAdapter) Descriptor() dap.AdapterDescriptor {
	return dap.AdapterDescriptor{
		Name:             "netcoredbg",
		Language:         dotnetLanguage,
		AdapterID:        "coreclr",
		Command:          "netcoredbg",
		Args:             []string{"--interpreter=vscode"},
		Transport:        dap.TransportStdio,
		Markers:          []string{"*.csproj", "*.sln", "*.slnx", "global.json"},
		SourceExtensions: []string{".cs"},
		Defaults:         map[string]any{"type": "coreclr"},
		ConfigurationPaths: []dap.ConfigurationPath{
			{Key: "program", AllowMissing: true},
			{Key: "cwd", Directory: true},
		},
	}
}

func (dotnetAdapter) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".cs")
}

func (dotnetAdapter) Detect(path string, source []byte) ([]Target, error) {
	masked := maskCStyleSource(source, true, false)
	match := csharpMainPattern.FindIndex(masked)
	offset := -1
	detail := ".NET Main method"
	if len(match) > 0 {
		relative := bytes.Index(masked[match[0]:match[1]], []byte("Main"))
		if relative >= 0 {
			offset = match[0] + relative
		}
	} else if strings.EqualFold(filepath.Base(filepath.FromSlash(path)), "Program.cs") && !containsCSharpNamespace(masked) {
		offset = csharpTopLevelOffset(masked)
		detail = ".NET top-level program"
	}
	if offset < 0 {
		return nil, nil
	}
	line, column := sourceLineColumn(source, offset)
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if directory == "" {
		directory = "."
	}
	return []Target{{
		ID:        fmt.Sprintf("dotnet:%s:main", filepath.ToSlash(path)),
		Name:      "Program",
		Detail:    detail,
		Kind:      "main",
		Language:  dotnetLanguage,
		Path:      filepath.ToSlash(path),
		Directory: directory,
		Line:      line,
		Column:    column,
	}}, nil
}

func containsCSharpNamespace(source []byte) bool {
	return regexp.MustCompile(`(?m)^\s*namespace\b`).Match(source)
}

func csharpTopLevelOffset(source []byte) int {
	offset := 0
	scanner := bufio.NewScanner(bytes.NewReader(source))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed != "" &&
			!strings.HasPrefix(trimmed, "using ") &&
			!strings.HasPrefix(trimmed, "global using ") &&
			!strings.HasPrefix(trimmed, "#") {
			if csharpTypeDeclarationPattern.MatchString(trimmed) {
				return -1
			}
			return offset + len(line) - len(strings.TrimLeft(line, " \t"))
		}
		offset += len(line) + 1
	}
	return -1
}

func (dotnetAdapter) Plan(request Request) (Plan, error) {
	if request.Target.Kind != "main" {
		return Plan{}, fmt.Errorf("unsupported .NET debug target kind %q", request.Target.Kind)
	}
	projectRoot := request.ProjectDir
	if !filepath.IsAbs(projectRoot) && request.WorkspaceDir != "" {
		projectRoot = filepath.Join(request.WorkspaceDir, filepath.FromSlash(projectRoot))
	}
	projectFile, err := findDotnetProject(projectRoot)
	if err != nil {
		return Plan{}, err
	}
	metadata, err := readDotnetProject(projectFile)
	if err != nil {
		return Plan{}, err
	}
	program, exists := dotnetProgramPath(filepath.Dir(projectFile), projectFile, metadata)
	relativeProgram, err := filepath.Rel(projectRoot, program)
	if err != nil || relativeProgram == ".." || strings.HasPrefix(relativeProgram, ".."+string(filepath.Separator)) {
		return Plan{}, errors.New("resolved .NET output is outside the selected project")
	}
	configuration := map[string]any{
		"program":    filepath.ToSlash(relativeProgram),
		"cwd":        ".",
		"justMyCode": true,
	}
	summary := fmt.Sprintf("%s .NET entry point %s.", actionLabel(request.Action), request.Target.Path)
	if !exists {
		summary = fmt.Sprintf("%s Build it with dotnet build first; the expected assembly is %s.", summary, filepath.ToSlash(relativeProgram))
	}
	plan := Plan{
		Title:         actionLabel(request.Action) + " " + request.Target.Name,
		Summary:       summary,
		ProjectDir:    request.ProjectDir,
		Request:       "launch",
		IO:            dap.IOOutput,
		Configuration: configuration,
	}
	if request.Action == "run" {
		configuration["noDebug"] = true
	} else {
		plan.Breakpoints = targetBreakpoint(request.Target)
	}
	return plan, nil
}

type dotnetProjectMetadata struct {
	AssemblyName    string
	TargetFramework string
	OutputPath      string
}

func findDotnetProject(projectRoot string) (string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return "", fmt.Errorf("read .NET project directory: %w", err)
	}
	var projects []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".csproj") {
			projects = append(projects, filepath.Join(projectRoot, entry.Name()))
		}
	}
	slices.Sort(projects)
	if len(projects) == 0 {
		return "", fmt.Errorf("no .csproj file was found in %s", filepath.ToSlash(projectRoot))
	}
	if len(projects) > 1 {
		return "", fmt.Errorf("multiple .csproj files were found in %s; choose a more specific project", filepath.ToSlash(projectRoot))
	}
	return projects[0], nil
}

func readDotnetProject(path string) (dotnetProjectMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return dotnetProjectMetadata{}, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	metadata := dotnetProjectMetadata{}
	decoder := xml.NewDecoder(io.LimitReader(file, maxSourceBytes))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return dotnetProjectMetadata{}, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var destination *string
		switch start.Name.Local {
		case "AssemblyName":
			if metadata.AssemblyName == "" {
				destination = &metadata.AssemblyName
			}
		case "TargetFramework":
			if metadata.TargetFramework == "" {
				destination = &metadata.TargetFramework
			}
		case "TargetFrameworks":
			if metadata.TargetFramework == "" {
				destination = &metadata.TargetFramework
			}
		case "OutputPath":
			if metadata.OutputPath == "" {
				destination = &metadata.OutputPath
			}
		}
		if destination != nil {
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return dotnetProjectMetadata{}, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
			}
			*destination = strings.TrimSpace(strings.Split(value, ";")[0])
		}
	}
	return metadata, nil
}

func dotnetProgramPath(projectRoot, projectFile string, metadata dotnetProjectMetadata) (string, bool) {
	assembly := metadata.AssemblyName
	if assembly == "" {
		assembly = strings.TrimSuffix(filepath.Base(projectFile), filepath.Ext(projectFile))
	}
	fileName := assembly + ".dll"
	output := metadata.OutputPath
	if output == "" || strings.Contains(output, "$(") {
		output = filepath.Join("bin", "Debug")
	}
	output = strings.ReplaceAll(output, "\\", string(filepath.Separator))
	if metadata.TargetFramework != "" && !strings.Contains(output, metadata.TargetFramework) {
		output = filepath.Join(output, metadata.TargetFramework)
	}
	expected := filepath.Join(projectRoot, filepath.FromSlash(output), fileName)
	if info, err := os.Stat(expected); err == nil && !info.IsDir() {
		return expected, true
	}
	if existing := findBuiltDotnetAssembly(projectRoot, fileName, metadata.TargetFramework); existing != "" {
		return existing, true
	}
	return expected, false
}

func findBuiltDotnetAssembly(projectRoot, fileName, targetFramework string) string {
	binDir := filepath.Join(projectRoot, "bin")
	var candidates []string
	_ = filepath.WalkDir(binDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if name == "ref" || name == "refint" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(entry.Name(), fileName) && pathContainsSegment(path, targetFramework) {
			candidates = append(candidates, path)
		}
		return nil
	})
	slices.SortFunc(candidates, func(left, right string) int {
		leftDebug := strings.Contains(strings.ToLower(filepath.ToSlash(left)), "/debug/")
		rightDebug := strings.Contains(strings.ToLower(filepath.ToSlash(right)), "/debug/")
		if leftDebug != rightDebug {
			if leftDebug {
				return -1
			}
			return 1
		}
		return strings.Compare(left, right)
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func pathContainsSegment(path, segment string) bool {
	if segment == "" {
		return true
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if strings.EqualFold(part, segment) {
			return true
		}
	}
	return false
}
