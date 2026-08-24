// Package dap provides workspace-scoped Debug Adapter Protocol sessions.
//
// Adapter-specific launch semantics stay outside this package. This package
// discovers adapter processes, frames messages, and manages generic protocol
// state.
package dap

import (
	"context"
	"io"
	"time"
)

type Transport string

const (
	TransportStdio   Transport = "stdio"
	TransportTCP     Transport = "tcp"
	TransportConnect Transport = "connect"
)

// IOMode controls where the debuggee reads input and writes program output.
// Adapter-specific launch values are mapped at the descriptor boundary.
type IOMode string

const (
	IOOutput   IOMode = "output"
	IOTerminal IOMode = "terminal"
)

// TerminalStrategy declares how an adapter integrates with an interactive
// terminal. It is adapter metadata, not language-specific launcher logic.
type TerminalStrategy string

const (
	TerminalUnsupported    TerminalStrategy = ""
	TerminalAdapterProcess TerminalStrategy = "adapterProcess"
	TerminalRunInTerminal  TerminalStrategy = "runInTerminal"
)

// TerminalLaunch is a direct, argument-preserving command requested by the
// DAP host or by an adapter through the standard runInTerminal request.
type TerminalLaunch struct {
	Title string
	Path  string
	Args  []string
	Dir   string
	Env   map[string]*string
}

type TerminalProcess interface {
	io.Closer
	ID() string
	ProcessID() int
	Done() <-chan struct{}
	Subscribe() (snapshot []byte, output <-chan []byte, cancel func())
}

type TerminalLauncher interface {
	LaunchTerminal(context.Context, TerminalLaunch) (TerminalProcess, error)
}

type AdapterConnector interface {
	ConnectAdapter(context.Context, Plan) (io.ReadWriteCloser, error)
}

// AdapterPlanPreparer optionally enriches a resolved plan before the adapter
// transport starts. It lets a host-created adapter obtain launch details from
// its owning service without putting language-specific behavior in Manager.
type AdapterPlanPreparer interface {
	PrepareAdapter(context.Context, Plan) (Plan, error)
}

// AdapterDescriptor describes how to start one debug adapter process. Command
// is resolved to an executable before a Plan reaches the session layer.
type AdapterDescriptor struct {
	Name               string
	Language           string
	AdapterID          string
	Command            string
	Args               []string
	Transport          Transport
	ReadyPrefix        string
	Markers            []string
	SourceExtensions   []string
	Defaults           map[string]any
	ConfigurationPaths []ConfigurationPath
	IOConfigKey        string
	IOValues           map[IOMode]string
	TargetConfigKey    string
	TerminalStrategy   TerminalStrategy
}

// ConfigurationPath identifies an adapter-owned configuration field whose
// value is a concrete path in the workspace. The generic launcher resolves
// relative values from ProjectDir before handing the configuration to the
// adapter. Fields that are commands, module names, or other opaque strings are
// deliberately not listed.
type ConfigurationPath struct {
	Key          string `json:"key"`
	Directory    bool   `json:"directory,omitempty"`
	AllowMissing bool   `json:"allow_missing,omitempty"`
}

// AdapterInfo is the detection result exposed to callers and launch planners.
type AdapterInfo struct {
	Name               string              `json:"name"`
	Language           string              `json:"language"`
	Command            string              `json:"command"`
	Projects           []string            `json:"projects"`
	ConfigurationPaths []ConfigurationPath `json:"configuration_paths,omitempty"`
	IOConfigKey        string              `json:"io_config_key,omitempty"`
	TerminalStrategy   TerminalStrategy    `json:"terminal_strategy,omitempty"`
}

// StartOptions carries a reviewed DAP launch or attach configuration.
// Configuration is intentionally open-ended because DAP delegates these
// arguments to each adapter.
type StartOptions struct {
	Adapter             string
	ProjectDir          string
	Request             string
	Configuration       map[string]any
	Breakpoints         map[string][]SourceBreakpoint
	FunctionBreakpoints []FunctionBreakpoint
	IO                  IOMode
	PreLaunch           *ProcessLaunch
	terminalLauncher    TerminalLauncher
	adapterConnector    AdapterConnector
}

// ProcessLaunch is a process owned by the debug session and started before
// the adapter. ReadyURL, when set, must respond before the adapter launches.
// WaitForExit instead runs the process to successful completion, such as a
// build step.
type ProcessLaunch struct {
	Title       string   `json:"title"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	ReadyURL    string   `json:"ready_url,omitempty"`
	WaitForExit bool     `json:"wait_for_exit,omitempty"`
}

type SourceBreakpoint struct {
	Line         int    `json:"line"`
	Column       int    `json:"column,omitempty"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hit_condition,omitempty"`
	LogMessage   string `json:"log_message,omitempty"`
}

type FunctionBreakpoint struct {
	Name         string `json:"name"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hit_condition,omitempty"`
}

// Plan is the resolved, internal form used to start an adapter process.
type Plan struct {
	Adapter    AdapterDescriptor
	ProjectDir string
	Target     string
	Mode       string
	Request    string
	IO         IOMode
	Arguments  map[string]any
	PreLaunch  *ProcessLaunch
}

type State string

const (
	StateStarting    State = "starting"
	StateConfiguring State = "configuring"
	StateRunning     State = "running"
	StateStopped     State = "stopped"
	StateTerminated  State = "terminated"
)

type Stop struct {
	Reason            string `json:"reason"`
	Description       string `json:"description,omitempty"`
	ThreadID          int    `json:"thread_id,omitempty"`
	AllThreadsStopped bool   `json:"all_threads_stopped,omitempty"`
	HitBreakpointIDs  []int  `json:"hit_breakpoint_ids,omitempty"`
}

type Status struct {
	SessionID    string       `json:"session_id"`
	Adapter      string       `json:"adapter"`
	Language     string       `json:"language"`
	Target       string       `json:"target,omitempty"`
	Mode         string       `json:"mode,omitempty"`
	Request      string       `json:"request"`
	IO           IOMode       `json:"io"`
	TerminalID   string       `json:"terminal_id,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	StateVersion uint64       `json:"state_version"`
	State        State        `json:"state"`
	Stop         *Stop        `json:"stop,omitempty"`
	ExitCode     *int         `json:"exit_code,omitempty"`
	StartedAt    time.Time    `json:"started_at"`
	Error        string       `json:"error,omitempty"`
}

type Capabilities struct {
	SupportsStepBack bool `json:"supports_step_back"`
}

type Thread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type StackFrame struct {
	ID     int     `json:"id"`
	Name   string  `json:"name"`
	Source *Source `json:"source,omitempty"`
	Line   int     `json:"line"`
	Column int     `json:"column"`
}

type Scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variables_reference"`
	NamedVariables     int    `json:"named_variables,omitempty"`
	IndexedVariables   int    `json:"indexed_variables,omitempty"`
	Expensive          bool   `json:"expensive,omitempty"`
}

type Variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	EvaluateName       string `json:"evaluate_name,omitempty"`
	VariablesReference int    `json:"variables_reference,omitempty"`
	NamedVariables     int    `json:"named_variables,omitempty"`
	IndexedVariables   int    `json:"indexed_variables,omitempty"`
}

type Evaluation struct {
	Result             string `json:"result"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variables_reference,omitempty"`
	NamedVariables     int    `json:"named_variables,omitempty"`
	IndexedVariables   int    `json:"indexed_variables,omitempty"`
}

type Breakpoint struct {
	ID       int    `json:"id,omitempty"`
	Verified bool   `json:"verified"`
	Message  string `json:"message,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}
