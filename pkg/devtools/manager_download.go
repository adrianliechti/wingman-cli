package devtools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type githubRelease struct {
	Assets []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

func (m *Manager) githubAsset(ctx context.Context, releaseURL, assetName string) ([]byte, error) {
	metadata, err := m.fetch(ctx, releaseURL)
	if err != nil {
		return nil, fmt.Errorf("query latest release: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(metadata, &release); err != nil {
		return nil, fmt.Errorf("decode latest release: %w", err)
	}
	for _, asset := range release.Assets {
		if asset.Name != assetName {
			continue
		}
		data, err := m.fetch(ctx, asset.URL)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", asset.Name, err)
		}
		if err := verifySHA256(data, asset.Digest); err != nil {
			return nil, fmt.Errorf("verify %s: %w", asset.Name, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("latest release has no %s asset", assetName)
}

func fetchURL(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "wingman-agent")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	return io.ReadAll(response.Body)
}

func verifySHA256(data []byte, published string) error {
	want := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(published)), "sha256:")
	if fields := strings.Fields(want); len(fields) > 0 {
		want = fields[0]
	}
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	if len(want) != len(got) || !strings.EqualFold(got, want) {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", got, published)
	}
	return nil
}

func extractTarGzip(data []byte, destination string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := archiveTarget(destination, header.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := writeArchiveFile(target, header.FileInfo().Mode().Perm(), io.LimitReader(tarReader, header.Size)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
}

func extractZip(data []byte, destination string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, entry := range reader.File {
		target, err := archiveTarget(destination, entry.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			if err := extractZipSymlink(entry, destination, target); err != nil {
				return err
			}
			continue
		}
		if !entry.FileInfo().Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry %q", entry.Name)
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		writeErr := writeArchiveFile(target, entry.Mode().Perm(), source)
		closeErr := source.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractZipSymlink(entry *zip.File, destination, target string) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	contents, readErr := io.ReadAll(source)
	closeErr := source.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	link := filepath.Clean(filepath.FromSlash(string(contents)))
	if link == "." || filepath.IsAbs(link) {
		return fmt.Errorf("archive symlink escapes destination: %q", entry.Name)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), link))
	relative, err := filepath.Rel(destination, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive symlink escapes destination: %q", entry.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, target)
}

func archiveTarget(destination, name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(name))
	if name == "." {
		return "", nil
	}
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path escapes destination: %q", name)
	}
	return filepath.Join(destination, name), nil
}

func writeArchiveFile(path string, mode os.FileMode, source io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	return errors.Join(copyErr, closeErr)
}
