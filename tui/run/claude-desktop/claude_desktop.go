package claudedesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/tui/run"
)

const (
	profileName = "Wingman"
	profileID   = "00000000-0000-4000-8000-000000000424"
)

type Options = run.Options

func Run(ctx context.Context, args []string, options *Options) error {
	restore, err := parseArgs(args)
	if err != nil {
		return err
	}

	if restore {
		if err := killClaude(); err != nil {
			return err
		}
		if err := Restore(); err != nil {
			return err
		}

		fmt.Fprintln(os.Stderr, "Claude Desktop restored to the usual Claude profile.")
		return nil
	}

	if err := killClaude(); err != nil {
		return err
	}
	if err := Configure(ctx, options); err != nil {
		return err
	}
	if err := openApp(); err != nil {
		_ = Restore()
		return err
	}

	fmt.Fprintln(os.Stderr, "Claude Desktop is running with Wingman. Press Ctrl-C to restore the usual Claude profile.")

	<-ctx.Done()

	fmt.Fprintln(os.Stderr, "Restoring Claude Desktop to the usual Claude profile.")

	if err := killClaude(); err != nil {
		return err
	}
	if err := Restore(); err != nil {
		return err
	}

	return nil
}

func Configure(ctx context.Context, options *Options) error {
	if err := supported(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cfg := newGatewayConfig(options)

	targets, err := targetPaths()
	if err != nil {
		return err
	}

	if err := writeDeploymentMode(targets.normalConfig, "3p"); err != nil {
		return err
	}

	target := targets.thirdPartyProfile
	if err := writeDeploymentMode(target.desktopConfig, "3p"); err != nil {
		return err
	}
	if err := writeMeta(target.meta); err != nil {
		return err
	}
	if err := writeGatewayProfile(target.profile, cfg.BaseURL, cfg.AuthToken); err != nil {
		return err
	}

	return nil
}

type gatewayConfig struct {
	BaseURL   string
	AuthToken string
}

func newGatewayConfig(options *Options) gatewayConfig {
	if options == nil {
		options = new(Options)
	}

	baseURL := options.WingmanURL
	if baseURL == "" {
		baseURL = os.Getenv("WINGMAN_URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:4242"
	}

	authToken := options.WingmanToken
	if authToken == "" {
		authToken = os.Getenv("WINGMAN_TOKEN")
	}
	if authToken == "" {
		authToken = "-"
	}

	return gatewayConfig{
		BaseURL:   baseURL,
		AuthToken: authToken,
	}
}

func Restore() error {
	if err := supported(); err != nil {
		return err
	}

	targets, err := targetPaths()
	if err != nil {
		return err
	}

	if err := writeDeploymentMode(targets.normalConfig, "1p"); err != nil {
		return err
	}

	target := targets.thirdPartyProfile
	if err := writeDeploymentMode(target.desktopConfig, "1p"); err != nil {
		return err
	}
	if err := restoreMeta(target.meta); err != nil {
		return err
	}
	if err := restoreProfile(target.profile); err != nil {
		return err
	}

	return nil
}

func parseArgs(args []string) (restore bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--restore":
			restore = true
		default:
			return false, fmt.Errorf("claude-desktop does not accept argument %q", arg)
		}
	}

	return restore, nil
}

type thirdPartyPaths struct {
	desktopConfig string
	meta          string
	profile       string
}

type targets struct {
	normalConfig      string
	thirdPartyProfile thirdPartyPaths
}

func targetPaths() (targets, error) {
	normalRoot, thirdPartyRoot, err := profileRoots()
	if err != nil {
		return targets{}, err
	}

	return targets{
		normalConfig: filepath.Join(normalRoot, "claude_desktop_config.json"),
		thirdPartyProfile: thirdPartyPaths{
			desktopConfig: filepath.Join(thirdPartyRoot, "claude_desktop_config.json"),
			meta:          filepath.Join(thirdPartyRoot, "configLibrary", "_meta.json"),
			profile:       filepath.Join(thirdPartyRoot, "configLibrary", profileID+".json"),
		},
	}, nil
}

func writeDeploymentMode(path, mode string) error {
	cfg, err := readJSONAllowMissing(path)
	if err != nil {
		return fmt.Errorf("parse Claude Desktop config: %w", err)
	}

	cfg["deploymentMode"] = mode
	return writeJSON(path, cfg)
}

func writeMeta(path string) error {
	meta, err := readJSONAllowMissing(path)
	if err != nil {
		return fmt.Errorf("parse Claude Desktop config metadata: %w", err)
	}

	meta["appliedId"] = profileID

	entries := make([]any, 0)
	for _, entry := range anySlice(meta["entries"]) {
		entryMap, _ := entry.(map[string]any)
		if entryMap == nil {
			entries = append(entries, entry)
			continue
		}
		if entryID, _ := entryMap["id"].(string); entryID == profileID {
			continue
		}
		entries = append(entries, entryMap)
	}

	entries = append(entries, map[string]any{
		"id":   profileID,
		"name": profileName,
	})
	meta["entries"] = entries

	return writeJSON(path, meta)
}

func writeGatewayProfile(path, baseURL, authToken string) error {
	cfg, err := readJSONAllowMissing(path)
	if err != nil {
		return fmt.Errorf("parse Claude Desktop Wingman profile: %w", err)
	}

	cfg["inferenceProvider"] = "gateway"
	cfg["inferenceGatewayBaseUrl"] = strings.TrimRight(baseURL, "/")
	cfg["inferenceGatewayApiKey"] = authToken
	cfg["inferenceGatewayAuthScheme"] = "bearer"
	cfg["deploymentOrganizationUuid"] = profileID
	cfg["disableDeploymentModeChooser"] = true
	delete(cfg, "inferenceModels")

	return writeJSON(path, cfg)
}

func restoreMeta(path string) error {
	meta, err := readJSONAllowMissing(path)
	if err != nil {
		return fmt.Errorf("parse Claude Desktop config metadata: %w", err)
	}
	if len(meta) == 0 {
		return nil
	}

	changed := false
	if appliedID, _ := meta["appliedId"].(string); appliedID == profileID {
		delete(meta, "appliedId")
		changed = true
	}

	if entries := anySlice(meta["entries"]); entries != nil {
		filtered := make([]any, 0, len(entries))
		for _, entry := range entries {
			entryMap, _ := entry.(map[string]any)
			if entryID, _ := entryMap["id"].(string); entryID == profileID {
				changed = true
				continue
			}
			filtered = append(filtered, entry)
		}
		meta["entries"] = filtered
	}

	if !changed {
		return nil
	}

	return writeJSON(path, meta)
}

func restoreProfile(path string) error {
	cfg, err := readJSONAllowMissing(path)
	if err != nil {
		return fmt.Errorf("parse Claude Desktop Wingman profile: %w", err)
	}
	if len(cfg) == 0 {
		return nil
	}

	cfg["disableDeploymentModeChooser"] = false
	delete(cfg, "inferenceProvider")
	delete(cfg, "inferenceGatewayBaseUrl")
	delete(cfg, "inferenceGatewayApiKey")
	delete(cfg, "inferenceGatewayAuthScheme")
	delete(cfg, "inferenceModels")

	return writeJSON(path, cfg)
}

func readJSONAllowMissing(path string) (map[string]any, error) {
	cfg, err := readJSON(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	return cfg, err
}

func readJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}

	return cfg, nil
}

func writeJSON(path string, cfg any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		if err := os.WriteFile(path+".bak", existing, 0644); err != nil {
			return err
		}
	}

	return os.WriteFile(path, data, 0644)
}

func anySlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case nil:
		return nil
	default:
		return nil
	}
}

func killClaude() error {
	if !isRunning() {
		return nil
	}
	if err := killApp(); err != nil {
		if !isRunning() {
			return nil
		}
		return fmt.Errorf("kill Claude Desktop: %w", err)
	}

	return waitForExit(30 * time.Second)
}

func waitForExit(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isRunning() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("Claude Desktop did not quit; quit it manually and re-run the command")
}
