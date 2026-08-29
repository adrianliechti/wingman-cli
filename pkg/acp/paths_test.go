package acp

import "testing"

func TestNormalizeSessionRoots(t *testing.T) {
	cwd, additional, err := NormalizeSessionRoots("/workspace/./app", []string{
		"/workspace/app", "/workspace/lib/../lib", "/workspace/lib",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cwd != "/workspace/app" || len(additional) != 1 || additional[0] != "/workspace/lib" {
		t.Fatalf("roots = %q, %q", cwd, additional)
	}

	for _, tc := range []struct {
		cwd        string
		additional []string
	}{
		{cwd: "relative"},
		{cwd: "/workspace/\x00bad"},
		{cwd: "/workspace", additional: []string{"relative"}},
		{cwd: "/workspace", additional: []string{""}},
		{cwd: "/workspace", additional: []string{"/workspace/\x00bad"}},
	} {
		if _, _, err := NormalizeSessionRoots(tc.cwd, tc.additional); err == nil {
			t.Fatalf("expected invalid roots to fail: %#v", tc)
		}
	}
}
