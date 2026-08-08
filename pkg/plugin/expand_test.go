package plugin

import "testing"

func TestExpandReplacesBothPlaceholders(t *testing.T) {
	got := expand("${PLUGIN_ROOT}/bin:${PLUGIN_DATA}/cache", "/root", "/data")

	if want := "/root/bin:/data/cache"; got != want {
		t.Fatalf("expand = %q, want %q", got, want)
	}
}

func TestExpandDoesNotRescanReplacementText(t *testing.T) {
	got := expand("${PLUGIN_ROOT}", "/weird/${PLUGIN_DATA}", "/data")

	if want := "/weird/${PLUGIN_DATA}"; got != want {
		t.Fatalf("expand = %q, want the replacement left literal", got)
	}
}

func TestExpandLeavesOtherPlaceholdersAlone(t *testing.T) {
	got := expand("${HOME}/$PLUGIN_ROOT/${PLUGIN_ROOTX}", "/root", "/data")

	if want := "${HOME}/$PLUGIN_ROOT/${PLUGIN_ROOTX}"; got != want {
		t.Fatalf("expand = %q, want %q", got, want)
	}
}

func TestExpandHandlesRepeatedOccurrences(t *testing.T) {
	got := expand("${PLUGIN_DATA}:${PLUGIN_DATA}:${PLUGIN_ROOT}", "/root", "/data")

	if want := "/data:/data:/root"; got != want {
		t.Fatalf("expand = %q, want %q", got, want)
	}
}
