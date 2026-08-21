package claudedesktop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteGatewayProfileConfiguresCurrentClaudeSurfaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"existing":"keep","inferenceModels":["legacy"]}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := writeGatewayProfile(path, "https://wingman.example/", "secret"); err != nil {
		t.Fatal(err)
	}

	cfg := readTestJSON(t, path)
	want := map[string]any{
		"inferenceProvider":            "gateway",
		"inferenceGatewayBaseUrl":      "https://wingman.example",
		"inferenceGatewayApiKey":       "secret",
		"inferenceGatewayAuthScheme":   "bearer",
		"deploymentOrganizationUuid":   profileID,
		"disableDeploymentModeChooser": true,
		"chatTabEnabled":               true,
		"coworkEgressAllowedHosts":     []any{"*"},
		"disableEssentialTelemetry":    true,
		"disableNonessentialTelemetry": true,
		"autoModeEnabled":              false,
	}
	for key, value := range want {
		if !reflect.DeepEqual(cfg[key], value) {
			t.Errorf("%s = %#v, want %#v", key, cfg[key], value)
		}
	}
	if cfg["existing"] != "keep" {
		t.Errorf("existing setting = %#v, want keep", cfg["existing"])
	}
	if _, ok := cfg["inferenceModels"]; ok {
		t.Error("inferenceModels should be removed")
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestConfigureTargetsActivatesThirdPartyModeLast(t *testing.T) {
	root := t.TempDir()
	targets := targets{
		normalConfig: filepath.Join(root, "normal.json"),
		thirdPartyProfile: thirdPartyPaths{
			desktopConfig: filepath.Join(root, "third-party.json"),
			meta:          filepath.Join(root, "library", "_meta.json"),
			profile:       filepath.Join(root, "library", profileID+".json"),
		},
	}
	if err := os.WriteFile(targets.normalConfig, []byte(`{"deploymentMode":"1p","existing":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targets.thirdPartyProfile.desktopConfig, []byte(`{"deploymentMode":`), 0644); err != nil {
		t.Fatal(err)
	}

	err := configureTargets(targets, gatewayConfig{BaseURL: "https://wingman.example", AuthToken: "secret"})
	if err == nil {
		t.Fatal("configureTargets succeeded with malformed third-party config")
	}

	normal := readTestJSON(t, targets.normalConfig)
	if normal["deploymentMode"] != "1p" || normal["existing"] != true {
		t.Fatalf("normal config was activated before the profile was ready: %#v", normal)
	}
}

func TestRestoreProfileRemovesWingmanOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"existing":"keep"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeGatewayProfile(path, "https://wingman.example", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := restoreProfile(path); err != nil {
		t.Fatal(err)
	}

	cfg := readTestJSON(t, path)
	for _, key := range []string{
		"inferenceProvider",
		"inferenceGatewayBaseUrl",
		"inferenceGatewayApiKey",
		"inferenceGatewayAuthScheme",
		"deploymentOrganizationUuid",
		"inferenceModels",
		"coworkEgressAllowedHosts",
		"disableEssentialTelemetry",
		"disableNonessentialTelemetry",
		"autoModeEnabled",
	} {
		if _, ok := cfg[key]; ok {
			t.Errorf("%s should be removed during restore", key)
		}
	}
	if cfg["disableDeploymentModeChooser"] != false {
		t.Errorf("disableDeploymentModeChooser = %#v, want false", cfg["disableDeploymentModeChooser"])
	}
	if cfg["existing"] != "keep" {
		t.Errorf("existing setting = %#v, want keep", cfg["existing"])
	}
}

func TestWriteJSONSkipsUnchangedContentAndWritesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := map[string]any{"value": "original"}
	if err := writeJSON(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unchanged write created a backup: %v", err)
	}

	if err := writeJSON(path, map[string]any{"value": "updated"}); err != nil {
		t.Fatal(err)
	}
	backup := readTestJSON(t, path+".bak")
	if backup["value"] != "original" {
		t.Fatalf("backup value = %#v, want original", backup["value"])
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if matched, _ := filepath.Match(".wingman-claude-desktop-*", entry.Name()); matched {
			t.Errorf("temporary file was not cleaned up: %s", entry.Name())
		}
	}
}

func readTestJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	cfg, err := readJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
