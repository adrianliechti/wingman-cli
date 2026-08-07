package graph

type Kind string

const (
	KindFunction    Kind = "function"
	KindMethod      Kind = "method"
	KindClass       Kind = "class"
	KindInterface   Kind = "interface"
	KindType        Kind = "type"
	KindConstructor Kind = "constructor"
	KindModule      Kind = "module"
	KindConstant    Kind = "constant"
	KindVariable    Kind = "variable"
)

type EdgeKind string

const (
	EdgeCalls      EdgeKind = "calls"
	EdgeInherits   EdgeKind = "inherits"
	EdgeImplements EdgeKind = "implements"
)

type Provenance string

const (
	ViaName      Provenance = "name"
	ViaLSP       Provenance = "lsp"
	ViaAmbiguous Provenance = "ambiguous"
)

type Node struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	Name      string `json:"name"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Lang      string `json:"lang"`
}

type Edge struct {
	From string     `json:"from"`
	To   string     `json:"to"`
	Kind EdgeKind   `json:"kind"`
	Via  Provenance `json:"via,omitempty"`
}

type Import struct {
	FromFile string `json:"from"`
	Path     string `json:"path"`
	ToModule string `json:"to,omitempty"`
}

// CoverageIssue records a source file that discovery found but the structural
// index could not fully process. Content search still scans these files and
// returns their hits as raw matches.
type CoverageIssue struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

func kindFromTag(tagKind string) (Kind, bool) {
	switch tagKind {
	case "definition.function":
		return KindFunction, true
	case "definition.method":
		return KindMethod, true
	case "definition.class":
		return KindClass, true
	case "definition.interface":
		return KindInterface, true
	case "definition.type":
		return KindType, true
	case "definition.constructor":
		return KindConstructor, true
	case "definition.module", "definition.namespace":
		return KindModule, true
	case "definition.constant":
		return KindConstant, true
	case "definition.variable":
		return KindVariable, true
	}
	return "", false
}

func isCallTag(tagKind string) bool {
	switch tagKind {
	case "reference.call", "reference.send":
		return true
	}
	return false
}
