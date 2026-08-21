package shell

import (
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Keep grammar availability under test while AST-backed policy is developed.
// Parsing successfully is not a safety decision: expansion semantics and the
// actual executor dialect still have to be supplied by the classifier.
func TestTreeSitterShellGrammarAvailability(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		lang *gotreesitter.Language
		bad  bool
	}{
		{"bash", `echo ${a="$"}${b="$a(touch /tmp/pwned)"}${b@P}`, grammars.BashLanguage(), false},
		{"bash malformed", `echo "unterminated`, grammars.BashLanguage(), true},
		{"powershell", `Write-Output $(Get-ChildItem $env:HOME)`, grammars.PowershellLanguage(), false},
		{"powershell malformed", `Write-Output "unterminated`, grammars.PowershellLanguage(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := gotreesitter.NewParser(tc.lang).Parse([]byte(tc.cmd))
			got := err != nil || tree == nil || tree.RootNode() == nil || tree.RootNode().HasErrorOrMissing()
			if got != tc.bad {
				t.Fatalf("syntax error = %v, want %v", got, tc.bad)
			}
		})
	}
}
