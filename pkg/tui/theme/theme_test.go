package theme

import (
	"strings"
	"testing"
)

func TestSignatureIncludesFullPalette(t *testing.T) {
	SetDark()
	base := Default.Signature()
	changed := Default
	changed.BrWhite.R++

	if base == changed.Signature() {
		t.Fatal("signature ignored bright white")
	}
	if strings.Contains(base, "%!") {
		t.Fatalf("malformed signature: %q", base)
	}
}
