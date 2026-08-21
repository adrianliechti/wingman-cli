package language

// SemanticToken is the expanded editor representation shared by the LSP and
// Tree-sitter providers. The LSP client itself keeps the protocol's raw,
// delta-encoded SemanticTokens value.
type SemanticToken struct {
	Line      int      `json:"line"`
	Character int      `json:"character"`
	Length    int      `json:"length"`
	Type      string   `json:"type"`
	Modifiers []string `json:"modifiers,omitempty"`
}
