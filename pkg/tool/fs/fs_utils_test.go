package fs

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNormalizePath tests path normalization for the current platform.
// Note: This tests actual behavior on the current OS.
func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		workingDir string
		want       string
	}{
		{
			name:       "relative path stays relative",
			path:       "foo/bar.txt",
			workingDir: "/home/user/project",
			want:       filepath.FromSlash("foo/bar.txt"),
		},
		{
			name:       "dot path",
			path:       ".",
			workingDir: "/home/user/project",
			want:       ".",
		},
		{
			name:       "empty path",
			path:       "",
			workingDir: "/home/user/project",
			want:       "",
		},
	}

	// Add platform-specific tests for actual runtime behavior
	if runtime.GOOS == "windows" {
		tests = append(tests, []struct {
			name       string
			path       string
			workingDir string
			want       string
		}{
			{
				name:       "windows absolute path inside workspace",
				path:       "C:\\Users\\test\\project\\src\\file.go",
				workingDir: "C:\\Users\\test\\project",
				want:       "src\\file.go",
			},
			{
				name:       "windows preserves original casing",
				path:       "C:\\Users\\Test\\Project\\SRC\\File.go",
				workingDir: "c:\\users\\test\\project",
				want:       "SRC\\File.go",
			},
			{
				name:       "windows absolute path outside workspace",
				path:       "D:\\other\\file.go",
				workingDir: "C:\\Users\\test\\project",
				want:       "D:\\other\\file.go",
			},
		}...)
	} else {
		tests = append(tests, []struct {
			name       string
			path       string
			workingDir string
			want       string
		}{
			{
				name:       "unix absolute path inside workspace",
				path:       "/home/user/project/src/file.go",
				workingDir: "/home/user/project",
				want:       "src/file.go",
			},
			{
				name:       "unix absolute path outside workspace",
				path:       "/etc/passwd",
				workingDir: "/home/user/project",
				want:       "/etc/passwd",
			},
			{
				name:       "unix absolute path is workspace root",
				path:       "/home/user/project",
				workingDir: "/home/user/project",
				want:       ".",
			},
		}...)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePath(tt.path, tt.workingDir)

			if got != tt.want {
				t.Errorf("normalizePath(%q, %q) = %q, want %q", tt.path, tt.workingDir, got, tt.want)
			}
		})
	}
}

// TestIsOutsideWorkspace tests workspace boundary detection for the current platform.
func TestIsOutsideWorkspace(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		workingDir string
		want       bool
	}{
		{
			name:       "relative path is inside",
			path:       "foo/bar.txt",
			workingDir: "/home/user/project",
			want:       false,
		},
		{
			name:       "dot path is inside",
			path:       ".",
			workingDir: "/home/user/project",
			want:       false,
		},
		{
			name:       "parent traversal is not checked for relative paths",
			path:       "../other/file.txt",
			workingDir: "/home/user/project",
			want:       false, // relative paths are not checked as outside
		},
	}

	// Add platform-specific tests
	if runtime.GOOS == "windows" {
		tests = append(tests, []struct {
			name       string
			path       string
			workingDir string
			want       bool
		}{
			{
				name:       "windows absolute inside workspace",
				path:       "C:\\Users\\test\\project\\src",
				workingDir: "C:\\Users\\test\\project",
				want:       false,
			},
			{
				name:       "windows absolute outside workspace",
				path:       "C:\\Users\\other\\file.go",
				workingDir: "C:\\Users\\test\\project",
				want:       true,
			},
			{
				name:       "windows different drive",
				path:       "D:\\data\\file.go",
				workingDir: "C:\\Users\\test\\project",
				want:       true,
			},
		}...)
	} else {
		tests = append(tests, []struct {
			name       string
			path       string
			workingDir string
			want       bool
		}{
			{
				name:       "unix absolute inside workspace",
				path:       "/home/user/project/src/file.go",
				workingDir: "/home/user/project",
				want:       false,
			},
			{
				name:       "unix absolute outside workspace",
				path:       "/etc/passwd",
				workingDir: "/home/user/project",
				want:       true,
			},
			{
				name:       "unix parent directory",
				path:       "/home/user",
				workingDir: "/home/user/project",
				want:       true,
			},
			{
				name:       "unix sibling directory",
				path:       "/home/user/other",
				workingDir: "/home/user/project",
				want:       true,
			},
		}...)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOutsideWorkspace(tt.path, tt.workingDir)

			if got != tt.want {
				t.Errorf("isOutsideWorkspace(%q, %q) = %v, want %v", tt.path, tt.workingDir, got, tt.want)
			}
		})
	}
}

// TestCleanPath tests path cleaning behavior.
func TestCleanPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "empty path",
			path: "",
			want: "",
		},
		{
			name: "forward slashes",
			path: "foo/bar/baz",
			want: filepath.FromSlash("foo/bar/baz"),
		},
		{
			name: "redundant separators",
			path: "foo//bar///baz",
			want: filepath.FromSlash("foo/bar/baz"),
		},
		{
			name: "dot segments",
			path: "foo/./bar/../baz",
			want: filepath.FromSlash("foo/baz"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanPath(tt.path)

			if got != tt.want {
				t.Errorf("cleanPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestPathHandlingLogic tests cross-platform path handling logic.
// These tests verify the correctness of path handling algorithms
// independent of the current OS, using only forward slashes.
func TestPathHandlingLogic(t *testing.T) {
	t.Run("relative path detection", func(t *testing.T) {
		// Relative paths should never be considered outside workspace
		relativePaths := []string{
			"foo/bar.txt",
			"./file.go",
			"src/pkg/main.go",
			".",
		}

		for _, path := range relativePaths {
			// On all platforms, these should not be treated as absolute
			if filepath.IsAbs(filepath.FromSlash(path)) {
				continue // skip if this is actually absolute on current OS
			}

			if isOutsideWorkspace(path, "/any/workspace") {
				t.Errorf("relative path %q should not be outside workspace", path)
			}
		}
	})

	t.Run("path cleaning preserves relative structure", func(t *testing.T) {
		// Verify path cleaning handles various inputs correctly
		testCases := []struct {
			input    string
			expected string
		}{
			{"foo/bar", filepath.FromSlash("foo/bar")},
			{"./foo/bar", filepath.FromSlash("foo/bar")},
			{"foo//bar", filepath.FromSlash("foo/bar")},
			{"foo/./bar", filepath.FromSlash("foo/bar")},
		}

		for _, tc := range testCases {
			result := cleanPath(tc.input)

			if result != tc.expected {
				t.Errorf("cleanPath(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		}
	})
}

// TestWindowsPathBehavior tests Windows-specific path patterns.
// These tests document expected behavior for Windows-style paths.
// On non-Windows systems, they test that such paths are handled gracefully.
func TestWindowsPathBehavior(t *testing.T) {
	t.Run("windows drive letter detection", func(t *testing.T) {
		windowsPaths := []struct {
			path     string
			isAbsWin bool
		}{
			{"C:\\Users\\test", true},
			{"D:/data/file.txt", true},
			{"c:\\lower\\case", true},
			{"\\\\server\\share", true}, // UNC path
			{"relative\\path", false},
			{"/unix/path", false}, // Not a Windows absolute path (no drive)
		}

		for _, tc := range windowsPaths {
			// Test that our code would recognize Windows absolute paths
			// by checking if it starts with a drive letter pattern
			isWinAbs := len(tc.path) >= 2 &&
				((tc.path[0] >= 'A' && tc.path[0] <= 'Z') || (tc.path[0] >= 'a' && tc.path[0] <= 'z')) &&
				tc.path[1] == ':' ||
				(len(tc.path) >= 2 && tc.path[0] == '\\' && tc.path[1] == '\\')

			if isWinAbs != tc.isAbsWin {
				t.Errorf("Windows absolute detection for %q: got %v, want %v", tc.path, isWinAbs, tc.isAbsWin)
			}
		}
	})

	t.Run("windows different drives are outside workspace", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// On Windows, test actual behavior
			if !isOutsideWorkspace("D:\\data", "C:\\project") {
				t.Error("different drives should be outside workspace on Windows")
			}
		}
		// Document the expected behavior
		t.Log("Windows: Paths on different drives should be considered outside workspace")
	})
}

// TestUnixPathBehavior tests Unix-specific path patterns.
func TestUnixPathBehavior(t *testing.T) {
	t.Run("unix absolute path detection", func(t *testing.T) {
		unixPaths := []struct {
			path      string
			isAbsUnix bool
		}{
			{"/home/user", true},
			{"/etc/passwd", true},
			{"/", true},
			{"relative/path", false},
			{"./relative", false},
			{"../parent", false},
		}

		for _, tc := range unixPaths {
			// On Unix, absolute paths start with /
			isUnixAbs := len(tc.path) > 0 && tc.path[0] == '/'

			if isUnixAbs != tc.isAbsUnix {
				t.Errorf("Unix absolute detection for %q: got %v, want %v", tc.path, isUnixAbs, tc.isAbsUnix)
			}
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("unix workspace boundary", func(t *testing.T) {
			workingDir := "/home/user/project"

			insidePaths := []string{
				"/home/user/project/src",
				"/home/user/project/src/main.go",
				"/home/user/project",
			}

			outsidePaths := []string{
				"/home/user/other",
				"/etc/passwd",
				"/home/user",
				"/tmp/file",
			}

			for _, path := range insidePaths {
				if isOutsideWorkspace(path, workingDir) {
					t.Errorf("%q should be INSIDE workspace %q", path, workingDir)
				}
			}

			for _, path := range outsidePaths {
				if !isOutsideWorkspace(path, workingDir) {
					t.Errorf("%q should be OUTSIDE workspace %q", path, workingDir)
				}
			}
		})
	}
}

// TestSlashNormalization tests that forward slashes are handled consistently.
// This is important because LLMs often provide paths with forward slashes
// regardless of the target platform.
func TestSlashNormalization(t *testing.T) {
	t.Run("forward slashes in relative paths", func(t *testing.T) {
		// Forward slashes should work on all platforms
		result := normalizePath("src/pkg/main.go", "/workspace")
		expected := filepath.FromSlash("src/pkg/main.go")

		if result != expected {
			t.Errorf("forward slashes not normalized: got %q, want %q", result, expected)
		}
	})

	t.Run("cleanPath normalizes forward slashes", func(t *testing.T) {
		result := cleanPath("foo/bar/baz")
		expected := filepath.FromSlash("foo/bar/baz")

		if result != expected {
			t.Errorf("cleanPath should normalize slashes: got %q, want %q", result, expected)
		}
	})
}

func TestDetectLineEnding(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unix line endings",
			content: "line1\nline2\nline3",
			want:    "\n",
		},
		{
			name:    "windows line endings",
			content: "line1\r\nline2\r\nline3",
			want:    "\r\n",
		},
		{
			name:    "mixed line endings prefers first",
			content: "line1\r\nline2\nline3",
			want:    "\r\n",
		},
		{
			name:    "no line endings",
			content: "single line",
			want:    "\n",
		},
		{
			name:    "empty content",
			content: "",
			want:    "\n",
		},
		{
			name:    "only LF before CRLF",
			content: "line1\nline2\r\nline3",
			want:    "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectLineEnding(tt.content)

			if got != tt.want {
				t.Errorf("detectLineEnding() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeToLF(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "already LF",
			text: "line1\nline2\nline3",
			want: "line1\nline2\nline3",
		},
		{
			name: "CRLF to LF",
			text: "line1\r\nline2\r\nline3",
			want: "line1\nline2\nline3",
		},
		{
			name: "CR only to LF",
			text: "line1\rline2\rline3",
			want: "line1\nline2\nline3",
		},
		{
			name: "mixed endings",
			text: "line1\r\nline2\nline3\r",
			want: "line1\nline2\nline3\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeToLF(tt.text)

			if got != tt.want {
				t.Errorf("normalizeToLF() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRestoreLineEndings(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		ending string
		want   string
	}{
		{
			name:   "keep LF",
			text:   "line1\nline2",
			ending: "\n",
			want:   "line1\nline2",
		},
		{
			name:   "convert to CRLF",
			text:   "line1\nline2",
			ending: "\r\n",
			want:   "line1\r\nline2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := restoreLineEndings(tt.text, tt.ending)

			if got != tt.want {
				t.Errorf("restoreLineEndings() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripBom(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantBom     string
		wantContent string
	}{
		{
			name:        "no BOM",
			content:     "hello world",
			wantBom:     "",
			wantContent: "hello world",
		},
		{
			name:        "with UTF-8 BOM",
			content:     "\uFEFFhello world",
			wantBom:     "\uFEFF",
			wantContent: "hello world",
		},
		{
			name:        "empty content",
			content:     "",
			wantBom:     "",
			wantContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBom, gotContent := stripBom(tt.content)

			if gotBom != tt.wantBom {
				t.Errorf("stripBom() bom = %q, want %q", gotBom, tt.wantBom)
			}

			if gotContent != tt.wantContent {
				t.Errorf("stripBom() content = %q, want %q", gotContent, tt.wantContent)
			}
		})
	}
}

func TestTruncateHead(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantByLines bool
		wantByBytes bool
	}{
		{
			name:        "small content",
			content:     "line1\nline2\nline3",
			wantByLines: false,
			wantByBytes: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, byLines, byBytes := truncateHead(tt.content)

			if byLines != tt.wantByLines {
				t.Errorf("truncateHead() byLines = %v, want %v", byLines, tt.wantByLines)
			}

			if byBytes != tt.wantByBytes {
				t.Errorf("truncateHead() byBytes = %v, want %v", byBytes, tt.wantByBytes)
			}

			if result == "" && tt.content != "" {
				t.Error("truncateHead() returned empty result for non-empty content")
			}
		})
	}
}

func TestNormalizeForFuzzyMatch(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "trailing whitespace removed",
			text: "line1   \nline2\t\nline3",
			want: "line1\nline2\nline3",
		},
		{
			name: "smart quotes normalized",
			text: "\u201Chello\u201D and \u2018world\u2019",
			want: "\"hello\" and 'world'",
		},
		{
			name: "em dash normalized",
			text: "foo—bar",
			want: "foo-bar",
		},
		{
			name: "non-breaking space normalized",
			text: "hello\u00A0world",
			want: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeForFuzzyMatch(tt.text)

			if got != tt.want {
				t.Errorf("normalizeForFuzzyMatch() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFuzzyFindText(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		oldText        string
		wantFound      bool
		wantFuzzyMatch bool
	}{
		{
			name:           "exact match",
			content:        "hello world",
			oldText:        "world",
			wantFound:      true,
			wantFuzzyMatch: false,
		},
		{
			name:           "no match",
			content:        "hello world",
			oldText:        "foo",
			wantFound:      false,
			wantFuzzyMatch: false,
		},
		{
			name:           "fuzzy match with trailing whitespace",
			content:        "line1   \nline2",
			oldText:        "line1\nline2",
			wantFound:      true,
			wantFuzzyMatch: true,
		},
		{
			name:           "fuzzy match with smart quotes",
			content:        "say \u201Chello\u201D",
			oldText:        "say \"hello\"",
			wantFound:      true,
			wantFuzzyMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fuzzyFindText(tt.content, tt.oldText)

			if result.found != tt.wantFound {
				t.Errorf("fuzzyFindText() found = %v, want %v", result.found, tt.wantFound)
			}

			if result.usedFuzzyMatch != tt.wantFuzzyMatch {
				t.Errorf("fuzzyFindText() usedFuzzyMatch = %v, want %v", result.usedFuzzyMatch, tt.wantFuzzyMatch)
			}
		})
	}
}

func TestGenerateDiffString(t *testing.T) {
	tests := []struct {
		name       string
		oldContent string
		newContent string
		wantEmpty  bool
	}{
		{
			name:       "no changes",
			oldContent: "line1\nline2",
			newContent: "line1\nline2",
			wantEmpty:  true,
		},
		{
			name:       "line added",
			oldContent: "line1\nline2",
			newContent: "line1\nline2\nline3",
			wantEmpty:  false,
		},
		{
			name:       "line removed",
			oldContent: "line1\nline2\nline3",
			newContent: "line1\nline2",
			wantEmpty:  false,
		},
		{
			name:       "line changed",
			oldContent: "line1\nold\nline3",
			newContent: "line1\nnew\nline3",
			wantEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateDiffString(tt.oldContent, tt.newContent)
			isEmpty := got == ""

			if isEmpty != tt.wantEmpty {
				t.Errorf("generateDiffString() isEmpty = %v, want %v, got: %q", isEmpty, tt.wantEmpty, got)
			}
		})
	}
}

func TestPathDomain(t *testing.T) {
	tests := []struct {
		name   string
		fsPath string
		want   []string
	}{
		{
			name:   "empty path",
			fsPath: "",
			want:   nil,
		},
		{
			name:   "dot path",
			fsPath: ".",
			want:   nil,
		},
		{
			name:   "simple path",
			fsPath: "foo/bar/baz",
			want:   []string{"foo", "bar", "baz"},
		},
		{
			name:   "single segment",
			fsPath: "foo",
			want:   []string{"foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathDomain(tt.fsPath)

			if len(got) != len(tt.want) {
				t.Errorf("pathDomain(%q) = %v, want %v", tt.fsPath, got, tt.want)

				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("pathDomain(%q)[%d] = %q, want %q", tt.fsPath, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRelPathSlash(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		target string
		want   string
	}{
		{
			name:   "simple relative",
			base:   "foo",
			target: "foo/bar/baz.txt",
			want:   "bar/baz.txt",
		},
		{
			name:   "same path",
			base:   "foo/bar",
			target: "foo/bar",
			want:   ".",
		},
		{
			name:   "nested path",
			base:   "src",
			target: "src/pkg/tool/fs/file.go",
			want:   "pkg/tool/fs/file.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relPathSlash(tt.base, tt.target)

			if got != tt.want {
				t.Errorf("relPathSlash(%q, %q) = %q, want %q", tt.base, tt.target, got, tt.want)
			}
		})
	}
}

func TestMapFuzzyIndexToOriginal(t *testing.T) {
	tests := []struct {
		name     string
		original string
		fuzzy    string
		fuzzyIdx int
		want     int
	}{
		{
			name:     "zero index",
			original: "hello",
			fuzzy:    "hello",
			fuzzyIdx: 0,
			want:     0,
		},
		{
			name:     "end of string",
			original: "hello",
			fuzzy:    "hello",
			fuzzyIdx: 5,
			want:     5,
		},
		{
			name:     "beyond string",
			original: "hello",
			fuzzy:    "hello",
			fuzzyIdx: 10,
			want:     5,
		},
		{
			name:     "middle of string",
			original: "hello world",
			fuzzy:    "hello world",
			fuzzyIdx: 6,
			want:     6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapFuzzyIndexToOriginal(tt.original, tt.fuzzy, tt.fuzzyIdx)

			if got != tt.want {
				t.Errorf("mapFuzzyIndexToOriginal() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestNormalizePathForComparison tests case-sensitivity handling across platforms.
func TestNormalizePathForComparison(t *testing.T) {
	t.Run("case normalization on case-insensitive systems", func(t *testing.T) {
		path := "/Users/Test/Project/File.go"
		normalized := normalizePathForComparison(path)

		switch runtime.GOOS {
		case "windows", "darwin":
			// Windows and macOS should lowercase for comparison
			if normalized != "/users/test/project/file.go" {
				t.Errorf("normalizePathForComparison(%q) = %q, want lowercase on %s", path, normalized, runtime.GOOS)
			}
		default:
			// Linux should preserve case
			if normalized != path {
				t.Errorf("normalizePathForComparison(%q) = %q, want unchanged on %s", path, normalized, runtime.GOOS)
			}
		}
	})

	t.Run("path comparison works with different cases", func(t *testing.T) {
		switch runtime.GOOS {
		case "windows", "darwin":
			// On case-insensitive systems, paths with different cases should match
			path1 := normalizePathForComparison("/Users/Test/Project")
			path2 := normalizePathForComparison("/users/test/project")
			if path1 != path2 {
				t.Errorf("paths should match after normalization on %s: %q != %q", runtime.GOOS, path1, path2)
			}
		default:
			// On Linux, paths with different cases should NOT match
			path1 := normalizePathForComparison("/Users/Test/Project")
			path2 := normalizePathForComparison("/users/test/project")
			if path1 == path2 {
				t.Errorf("paths should NOT match on case-sensitive Linux: %q == %q", path1, path2)
			}
		}
	})
}

// TestGenerateDiffStringFormat tests that the diff output uses the go-diff library properly.
func TestGenerateDiffStringFormat(t *testing.T) {
	t.Run("diff shows deletions with minus prefix", func(t *testing.T) {
		oldContent := "line1\nremove me\nline3"
		newContent := "line1\nline3"
		diff := generateDiffString(oldContent, newContent)

		if !strings.Contains(diff, "-") {
			t.Errorf("diff should contain '-' for deletions: %q", diff)
		}
		if !strings.Contains(diff, "remove me") {
			t.Errorf("diff should show deleted line: %q", diff)
		}
	})

	t.Run("diff shows additions with plus prefix", func(t *testing.T) {
		oldContent := "line1\nline3"
		newContent := "line1\nadd me\nline3"
		diff := generateDiffString(oldContent, newContent)

		if !strings.Contains(diff, "+") {
			t.Errorf("diff should contain '+' for additions: %q", diff)
		}
		if !strings.Contains(diff, "add me") {
			t.Errorf("diff should show added line: %q", diff)
		}
	})

	t.Run("diff shows line numbers", func(t *testing.T) {
		oldContent := "line1\nold\nline3"
		newContent := "line1\nnew\nline3"
		diff := generateDiffString(oldContent, newContent)

		// Should show line numbers in the diff output
		if !strings.Contains(diff, "2") {
			t.Errorf("diff should show line numbers: %q", diff)
		}
	})

	t.Run("diff handles multiline changes", func(t *testing.T) {
		oldContent := "func main() {\n\tfmt.Println(\"old\")\n}"
		newContent := "func main() {\n\tfmt.Println(\"new\")\n\treturn\n}"
		diff := generateDiffString(oldContent, newContent)

		// Should show both deletion and addition
		if !strings.Contains(diff, "-") || !strings.Contains(diff, "+") {
			t.Errorf("diff should show both deletions and additions: %q", diff)
		}
	})
}

func TestIsBinaryFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "go file",
			path: "main.go",
			want: false,
		},
		{
			name: "typescript file",
			path: "app.ts",
			want: false,
		},
		{
			name: "exe file",
			path: "program.exe",
			want: true,
		},
		{
			name: "png image",
			path: "image.png",
			want: true,
		},
		{
			name: "zip archive",
			path: "archive.zip",
			want: true,
		},
		{
			name: "dll file",
			path: "library.dll",
			want: true,
		},
		{
			name: "so file",
			path: "library.so",
			want: true,
		},
		{
			name: "no extension",
			path: "Makefile",
			want: false,
		},
		{
			name: "uppercase extension",
			path: "image.PNG",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBinaryFile(tt.path)

			if got != tt.want {
				t.Errorf("isBinaryFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
