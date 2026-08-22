package devtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	jdtlsMilestonesURL = "https://download.eclipse.org/jdtls/milestones/"
	javaDebugLatestURL = "https://open-vsx.org/api/vscjava/vscode-java-debug/latest"
)

var (
	javaRecipes = []recipe{
		{ID: "jdtls", Kind: installerJava, Commands: []string{"jdtls"}},
	}
	jdtlsVersionPattern = regexp.MustCompile(`/jdtls/milestones/([0-9]+\.[0-9]+\.[0-9]+)`)
)

type openVSXExtension struct {
	Verified bool `json:"verified"`
	Files    struct {
		Download string `json:"download"`
		SHA256   string `json:"sha256"`
	} `json:"files"`
}

func (m *Manager) installJava(ctx context.Context, item recipe, stage string) error {
	if item.ID != "jdtls" {
		return fmt.Errorf("unknown Java tool %q", item.ID)
	}
	if err := m.installJDTLS(ctx, stage); err != nil {
		return err
	}
	if err := m.installJavaDebug(ctx, stage); err != nil {
		return err
	}
	return nil
}

func (m *Manager) installJDTLS(ctx context.Context, stage string) error {
	index, err := m.fetch(ctx, jdtlsMilestonesURL)
	if err != nil {
		return fmt.Errorf("query JDT LS milestones: %w", err)
	}
	version := latestJDTLSVersion(index)
	if version == "" {
		return errors.New("JDT LS milestone index contains no release")
	}
	baseURL := jdtlsMilestonesURL + version + "/"
	latest, err := m.fetch(ctx, baseURL+"latest.txt")
	if err != nil {
		return fmt.Errorf("query JDT LS %s archive: %w", version, err)
	}
	filename := strings.TrimSpace(string(latest))
	if filename != filepath.Base(filename) || !strings.HasPrefix(filename, "jdt-language-server-") || !strings.HasSuffix(filename, ".tar.gz") {
		return fmt.Errorf("invalid JDT LS archive name %q", filename)
	}
	archive, err := m.fetch(ctx, baseURL+filename)
	if err != nil {
		return fmt.Errorf("download %s: %w", filename, err)
	}
	checksum, err := m.fetch(ctx, baseURL+filename+".sha256")
	if err != nil {
		return fmt.Errorf("download %s checksum: %w", filename, err)
	}
	if err := verifySHA256(archive, string(checksum)); err != nil {
		return fmt.Errorf("verify %s: %w", filename, err)
	}
	if err := extractTarGzip(archive, stage); err != nil {
		return fmt.Errorf("extract %s: %w", filename, err)
	}
	return nil
}

func (m *Manager) installJavaDebug(ctx context.Context, stage string) error {
	metadata, err := m.fetch(ctx, javaDebugLatestURL)
	if err != nil {
		return fmt.Errorf("query latest java-debug package: %w", err)
	}
	var extension openVSXExtension
	if err := json.Unmarshal(metadata, &extension); err != nil {
		return fmt.Errorf("decode latest java-debug package: %w", err)
	}
	if !extension.Verified || extension.Files.Download == "" || extension.Files.SHA256 == "" {
		return errors.New("latest java-debug package is not verified or has no checksum")
	}
	archive, err := m.fetch(ctx, extension.Files.Download)
	if err != nil {
		return fmt.Errorf("download java-debug package: %w", err)
	}
	checksum, err := m.fetch(ctx, extension.Files.SHA256)
	if err != nil {
		return fmt.Errorf("download java-debug checksum: %w", err)
	}
	if err := verifySHA256(archive, string(checksum)); err != nil {
		return fmt.Errorf("verify java-debug package: %w", err)
	}
	destination := filepath.Join(stage, "java-debug")
	if err := extractZip(archive, destination); err != nil {
		return fmt.Errorf("extract java-debug package: %w", err)
	}
	if len(javaDebugBundlesAt(stage)) == 0 {
		return errors.New("java-debug package contains no JDT LS plug-in")
	}
	return nil
}

func latestJDTLSVersion(index []byte) string {
	var best [3]int
	found := false
	for _, match := range jdtlsVersionPattern.FindAllSubmatch(index, -1) {
		parts := strings.Split(string(match[1]), ".")
		version := [3]int{}
		for i := range version {
			version[i], _ = strconv.Atoi(parts[i])
		}
		if !found || version[0] > best[0] ||
			version[0] == best[0] && version[1] > best[1] ||
			version[0] == best[0] && version[1] == best[1] && version[2] > best[2] {
			best = version
			found = true
		}
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", best[0], best[1], best[2])
}

// JavaDebugBundles returns the managed java-debug plug-in for JDT LS.
func (m *Manager) JavaDebugBundles() []string {
	if m == nil {
		return nil
	}
	return javaDebugBundlesAt(filepath.Join(m.root, "jdtls"))
}

func javaDebugBundlesAt(root string) []string {
	pattern := filepath.Join(root, "java-debug", "extension", "server", "com.microsoft.java.debug.plugin-*.jar")
	matches, _ := filepath.Glob(pattern)
	slices.Sort(matches)
	if len(matches) == 0 {
		return nil
	}
	return []string{matches[len(matches)-1]}
}
