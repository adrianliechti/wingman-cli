package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestCredentialStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "creds.json")
	store := NewCredentialStore(path)

	cred, err := store.Get("https://example.com/mcp")
	if err != nil || cred != nil {
		t.Fatalf("Get on a missing file = %+v, %v", cred, err)
	}

	want := &Credential{ClientID: "abc", TokenURL: "https://as/token", Token: &oauth2.Token{AccessToken: "tok", RefreshToken: "ref", Expiry: time.Now().Add(time.Hour).Round(0)}}
	if err := store.Set("https://example.com/mcp", want); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", info.Mode().Perm())
		}
	}

	got, err := store.Get("https://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "abc" || got.Token.AccessToken != "tok" || got.Token.RefreshToken != "ref" || !got.Token.Valid() {
		t.Fatalf("got = %+v", got)
	}

	removed, err := store.Delete("https://example.com/mcp")
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v", removed, err)
	}
	removed, err = store.Delete("https://example.com/mcp")
	if err != nil || removed {
		t.Fatalf("second Delete = %v, %v", removed, err)
	}
}

func TestCredentialUsableToken(t *testing.T) {
	expired := time.Now().Add(-time.Minute)

	cases := []struct {
		name string
		cred Credential
		want bool
	}{
		{"none", Credential{}, false},
		{"valid", Credential{Token: &oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}}, true},
		{"expired without refresh", Credential{Token: &oauth2.Token{AccessToken: "a", Expiry: expired}}, false},
		{"expired with refresh", Credential{Token: &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: expired}}, true},
	}

	for _, tc := range cases {
		if got := tc.cred.usableToken(); got != tc.want {
			t.Errorf("%s: usableToken = %v, want %v", tc.name, got, tc.want)
		}
	}
}
