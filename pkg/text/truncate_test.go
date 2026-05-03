package text

import (
	"strings"
	"testing"
)

func TestTruncateMiddleEmpty(t *testing.T) {
	if got := TruncateMiddle("", 100); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTruncateMiddleUnderCap(t *testing.T) {
	in := "hello world"
	if got := TruncateMiddle(in, 100); got != in {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestTruncateMiddleExactCap(t *testing.T) {
	in := strings.Repeat("a", 100)
	if got := TruncateMiddle(in, 100); got != in {
		t.Errorf("expected unchanged at exact cap, got len=%d", len(got))
	}
}

func TestTruncateMiddleOverCap(t *testing.T) {
	in := strings.Repeat("a", 1000)
	got := TruncateMiddle(in, 100)

	if !strings.HasPrefix(got, strings.Repeat("a", 50)) {
		t.Errorf("expected head of 50 a's, got prefix %q", got[:60])
	}
	if !strings.HasSuffix(got, strings.Repeat("a", 50)) {
		t.Errorf("expected tail of 50 a's, got suffix %q", got[len(got)-60:])
	}
	if !strings.Contains(got, "chars truncated") {
		t.Errorf("expected marker in result, got %q", got)
	}
	if !strings.Contains(got, "900 chars truncated") {
		t.Errorf("expected '900 chars truncated' in result, got %q", got)
	}
}

func TestTruncateMiddleHeadAndTailPresent(t *testing.T) {
	in := "HEAD" + strings.Repeat("x", 1000) + "TAIL"
	got := TruncateMiddle(in, 50)

	if !strings.HasPrefix(got, "HEAD") {
		t.Errorf("expected HEAD prefix, got %q", got[:20])
	}
	if !strings.HasSuffix(got, "TAIL") {
		t.Errorf("expected TAIL suffix, got %q", got[len(got)-20:])
	}
}

func TestTruncateMiddleUTF8Boundaries(t *testing.T) {
	in := strings.Repeat("☃", 200) // 600 bytes
	got := TruncateMiddle(in, 60)

	if !isValidUTF8(got) {
		t.Errorf("output is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "chars truncated") {
		t.Errorf("expected marker in result, got %q", got)
	}
	if !strings.HasPrefix(got, "☃") {
		t.Errorf("expected head to start with ☃, got %q", got[:10])
	}
	if !strings.HasSuffix(got, "☃") {
		t.Errorf("expected tail to end with ☃, got %q", got[len(got)-10:])
	}
}

func TestTruncateMiddleZeroBudget(t *testing.T) {
	in := strings.Repeat("a", 100)
	got := TruncateMiddle(in, 0)

	if !strings.Contains(got, "100 chars truncated") {
		t.Errorf("expected full count in marker, got %q", got)
	}
}

func TestTruncateMiddleRemovedCharsCount(t *testing.T) {
	in := strings.Repeat("☃", 100) // 100 chars / 300 bytes
	got := TruncateMiddle(in, 30)

	if !strings.Contains(got, " chars truncated") {
		t.Errorf("expected 'chars truncated' marker, got %q", got)
	}

	// Confirm the reported number is reasonable (not 270, which would be bytes).
	for n := 80; n <= 95; n++ {
		if strings.Contains(got, itoa(n)+" chars truncated") {
			return
		}
	}
	t.Errorf("expected dropped-char count near 90 (not byte count), got %q", got)
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
