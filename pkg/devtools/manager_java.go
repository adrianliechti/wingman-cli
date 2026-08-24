package devtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	mavenLatestVersion           = "LATEST"
	mavenDependencyPluginVersion = "3.11.0"
	jdtlsMavenRepository         = "https://repo.eclipse.org/repository/ls-maven2-releases/"
	jdtlsCoreBundlePrefix        = "org.eclipse.jdt.ls.core_"
	javaDebugBundlePrefix        = "com.microsoft.java.debug.plugin-"
)

var javaRecipes = []recipe{
	{ID: "jdtls", Label: "Java language tools", Kind: installerMaven, Commands: []string{"jdtls"}},
	{ID: "java-debug", Label: "Java debugger", Kind: installerMaven, Commands: []string{"java-debug-adapter"}},
}

// installMaven installs Java tooling through the project wrapper or Maven.
// Repository selection remains subject to settings.xml mirrors, credentials,
// and proxy policy.
func (m *Manager) installMaven(ctx context.Context, item recipe, stage string) (string, error) {
	switch item.ID {
	case "jdtls":
		return m.installJDTLSMaven(ctx, item, stage)
	case "java-debug":
		return m.installJavaDebugMaven(ctx, item, stage)
	default:
		return "", fmt.Errorf("unknown Maven tool %q", item.ID)
	}
}

func (m *Manager) installJDTLSMaven(ctx context.Context, item recipe, stage string) (string, error) {
	maven, workingDir, err := m.mavenCommand(item, stage)
	if err != nil {
		return "", err
	}
	pom, err := writeJDTLSPOM(stage)
	if err != nil {
		return "", err
	}
	markers := filepath.Join(stage, ".maven-markers")
	args := []string{
		"--file", pom,
		"--batch-mode",
		"--no-transfer-progress",
		"--update-snapshots",
		"org.apache.maven.plugins:maven-dependency-plugin:" + mavenDependencyPluginVersion + ":unpack-dependencies",
		"-DincludeGroupIds=org.eclipse.jdt.ls",
		"-DincludeArtifactIds=org.eclipse.jdt.ls.product",
		"-DoutputDirectory=" + stage,
		"-DmarkersDirectory=" + markers,
		"-DexcludeTransitive=true",
	}
	if output, err := m.run(ctx, maven, args, workingDir, os.Environ()); err != nil {
		return "", commandError(output, err)
	}
	if err := errors.Join(os.RemoveAll(markers), os.Remove(pom)); err != nil {
		return "", fmt.Errorf("remove temporary Maven files: %w", err)
	}

	launcher := resolveInstalledCommand(stage, "jdtls")
	if launcher == "" && runtime.GOOS != "windows" {
		candidate := filepath.Join(stage, "bin", "jdtls")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			if chmodErr := os.Chmod(candidate, 0o755); chmodErr != nil {
				return "", fmt.Errorf("make JDT LS launcher executable: %w", chmodErr)
			}
			launcher = resolveInstalledCommand(stage, "jdtls")
		}
	}
	if launcher == "" {
		return "", errors.New("Maven artifact did not contain the JDT LS launcher")
	}
	version, err := jdtlsProductVersion(stage)
	if err != nil {
		return "", err
	}
	if version == m.installedVersion(item) {
		return "", errUpToDate
	}
	return version, nil
}

// A declared repository is required for Maven to resolve dynamic version
// metadata. Keeping it in a minimal POM also ensures settings.xml mirror rules
// apply to the Eclipse repository exactly as they do for normal projects.
func writeJDTLSPOM(root string) (string, error) {
	path := filepath.Join(root, ".wingman-jdtls-pom.xml")
	contents := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <groupId>dev.wingman</groupId>
  <artifactId>managed-jdtls</artifactId>
  <version>1</version>
  <repositories>
    <repository>
      <id>eclipse-jdtls</id>
      <url>` + jdtlsMavenRepository + `</url>
      <releases><enabled>true</enabled></releases>
      <snapshots><enabled>false</enabled></snapshots>
    </repository>
  </repositories>
  <dependencies>
    <dependency>
      <groupId>org.eclipse.jdt.ls</groupId>
      <artifactId>org.eclipse.jdt.ls.product</artifactId>
      <version>` + mavenLatestVersion + `</version>
      <type>tar.gz</type>
    </dependency>
  </dependencies>
</project>
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return "", fmt.Errorf("write JDT LS Maven project: %w", err)
	}
	return path, nil
}

// installJavaDebugMaven copies Microsoft's OSGi plug-in from Maven Central.
// Maven's dynamic version asks the configured repository or mirror for its
// newest available release, matching the latest-only policy used for JDT LS.
func (m *Manager) installJavaDebugMaven(ctx context.Context, item recipe, stage string) (string, error) {
	maven, workingDir, err := m.mavenCommand(item, stage)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(stage, "java-debug")
	artifact := "com.microsoft.java:com.microsoft.java.debug.plugin:" + mavenLatestVersion
	args := []string{
		"--batch-mode",
		"--no-transfer-progress",
		"--update-snapshots",
		"org.apache.maven.plugins:maven-dependency-plugin:" + mavenDependencyPluginVersion + ":copy",
		"-Dartifact=" + artifact,
		"-DoutputDirectory=" + destination,
		"-Dtransitive=false",
	}
	if output, err := m.run(ctx, maven, args, workingDir, os.Environ()); err != nil {
		return "", commandError(output, err)
	}
	version, err := javaDebugBundleVersion(stage)
	if err != nil {
		return "", err
	}
	if version == m.installedVersion(item) {
		return "", errUpToDate
	}
	if err := writeJavaDebugMarker(stage); err != nil {
		return "", fmt.Errorf("write Java debug adapter marker: %w", err)
	}
	return version, nil
}

// jdtlsProductVersion identifies the resolved release from its core OSGi
// bundle. Unlike the open Maven version range, this is a stable value that can
// be compared on the next refresh.
func jdtlsProductVersion(root string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return "", fmt.Errorf("read JDT LS plugins: %w", err)
	}
	version := ""
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, jdtlsCoreBundlePrefix) || !strings.HasSuffix(name, ".jar") {
			continue
		}
		candidate := strings.TrimSuffix(strings.TrimPrefix(name, jdtlsCoreBundlePrefix), ".jar")
		if candidate > version {
			version = candidate
		}
	}
	if version == "" {
		return "", errors.New("Maven artifact did not contain the JDT LS core bundle")
	}
	return version, nil
}

// Java debug runs inside JDT LS. The marker is intentionally only an
// availability token for DAP discovery; the Java connector never executes it.
func writeJavaDebugMarker(root string) error {
	directory := filepath.Join(root, "bin")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return os.WriteFile(filepath.Join(directory, "java-debug-adapter.cmd"), []byte("@echo off\r\necho java-debug is hosted by JDT LS 1^>^&2\r\nexit /b 1\r\n"), 0o755)
	}
	return os.WriteFile(filepath.Join(directory, "java-debug-adapter"), []byte("#!/bin/sh\necho 'java-debug is hosted by JDT LS' >&2\nexit 1\n"), 0o755)
}

func javaDebugBundlesAt(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "java-debug"))
	if err != nil {
		return nil
	}
	var bundles []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, javaDebugBundlePrefix) || !strings.HasSuffix(name, ".jar") {
			continue
		}
		bundles = append(bundles, filepath.Join(root, "java-debug", name))
	}
	slices.Sort(bundles)
	return bundles
}

func javaDebugBundleVersion(root string) (string, error) {
	bundles := javaDebugBundlesAt(root)
	if len(bundles) == 0 {
		return "", errors.New("Maven artifact did not contain the Java debug plug-in")
	}
	name := filepath.Base(bundles[len(bundles)-1])
	return strings.TrimSuffix(strings.TrimPrefix(name, javaDebugBundlePrefix), ".jar"), nil
}

func (m *Manager) mavenCommand(item recipe, fallback string) (command, workingDir string, err error) {
	name := "mvnw"
	if runtime.GOOS == "windows" {
		name = "mvnw.cmd"
	}
	for _, directory := range item.WorkingDirs {
		wrapper := filepath.Join(directory, name)
		if runtime.GOOS == "windows" {
			if info, err := os.Stat(wrapper); err == nil && info.Mode().IsRegular() {
				return wrapper, directory, nil
			}
		} else if info, err := os.Stat(wrapper); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return wrapper, directory, nil
		}
	}
	maven, err := m.look("mvn")
	if err != nil {
		return "", "", errors.New("Maven is not installed and this project has no Maven wrapper")
	}
	return maven, installWorkingDir(item, fallback), nil
}
