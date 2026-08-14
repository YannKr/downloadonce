package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSessionSecretRejectsEveryPublishedPlaceholder(t *testing.T) {
	for _, secret := range PlaceholderSecrets {
		c := &Config{DataDir: t.TempDir(), SessionSecret: secret}
		if err := c.ResolveSessionSecret(); err == nil {
			t.Errorf("published placeholder %q was accepted", secret)
		}
	}
}

// The value .env.example used to ship. It is 36 bytes, so a length check alone
// lets it through.
func TestResolveSessionSecretRejectsTheShippedExampleValue(t *testing.T) {
	const shipped = "change-me-before-going-to-production"
	if len(shipped) < MinSecretLen {
		t.Fatalf("this test is pointless if the value is short: %d bytes", len(shipped))
	}
	c := &Config{DataDir: t.TempDir(), SessionSecret: shipped}
	if err := c.ResolveSessionSecret(); err == nil {
		t.Fatal("the secret shipped in .env.example was accepted")
	}
}

func TestIsPlaceholderSecret(t *testing.T) {
	for _, s := range []string{
		"change-me-in-production-32-bytes!",
		"change-me-before-going-to-production",
		"CHANGE-ME-BEFORE-GOING-TO-PRODUCTION",
		"  change-me  ",
		"changeme",
		"change_me_now_please_this_is_long_enough",
	} {
		if !IsPlaceholderSecret(s) {
			t.Errorf("IsPlaceholderSecret(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"0123456789abcdef0123456789abcdef",
		"an-exchange-mechanism-for-tokens",
	} {
		if IsPlaceholderSecret(s) {
			t.Errorf("IsPlaceholderSecret(%q) = true, want false", s)
		}
	}
}

// .env.example must not ship a usable SESSION_SECRET.
func TestEnvExampleShipsNoSessionSecret(t *testing.T) {
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SESSION_SECRET=") {
			continue
		}
		value := strings.TrimPrefix(line, "SESSION_SECRET=")
		t.Fatalf(".env.example ships SESSION_SECRET=%q; it must be commented out or empty", value)
	}
}

func TestResolveSessionSecretRejectsShortSecret(t *testing.T) {
	c := &Config{DataDir: t.TempDir(), SessionSecret: "too-short"}
	if err := c.ResolveSessionSecret(); err == nil {
		t.Fatal("short secret was accepted")
	}
}

func TestResolveSessionSecretKeepsAGoodSecret(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	c := &Config{DataDir: t.TempDir(), SessionSecret: secret}
	if err := c.ResolveSessionSecret(); err != nil {
		t.Fatalf("good secret rejected: %v", err)
	}
	if c.SessionSecret != secret {
		t.Fatalf("secret = %q, want %q", c.SessionSecret, secret)
	}
}

func TestResolveSessionSecretGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()

	c := &Config{DataDir: dir}
	if err := c.ResolveSessionSecret(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(c.SessionSecret) < MinSecretLen {
		t.Fatalf("generated secret is %d bytes, want at least %d", len(c.SessionSecret), MinSecretLen)
	}

	path := filepath.Join(dir, "session_secret")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat secret file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("secret file mode = %04o, want 0600", perm)
	}

	// A restart must reuse the same secret, otherwise every restart logs
	// everyone out.
	c2 := &Config{DataDir: dir}
	if err := c2.ResolveSessionSecret(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c2.SessionSecret != c.SessionSecret {
		t.Fatal("second start generated a different secret")
	}
}

func TestParsePrefixes(t *testing.T) {
	got := parsePrefixes("127.0.0.1, 10.0.0.0/8, ::1, nonsense, ")
	if len(got) != 3 {
		t.Fatalf("parsed %d prefixes %v, want 3", len(got), got)
	}
	if got[0].String() != "127.0.0.1/32" {
		t.Errorf("bare IPv4 became %s, want 127.0.0.1/32", got[0])
	}
	if got[1].String() != "10.0.0.0/8" {
		t.Errorf("CIDR became %s, want 10.0.0.0/8", got[1])
	}
	if got[2].String() != "::1/128" {
		t.Errorf("bare IPv6 became %s, want ::1/128", got[2])
	}
	if len(parsePrefixes("")) != 0 {
		t.Error("empty TRUSTED_PROXIES produced prefixes")
	}
}

// A cap only caps if its default is a real limit.
func TestMaxUploadBytesDefaultIsBounded(t *testing.T) {
	os.Unsetenv("MAX_UPLOAD_BYTES")
	cfg := Load()
	const tenGB = int64(10) * 1024 * 1024 * 1024
	if cfg.MaxUploadBytes <= 0 || cfg.MaxUploadBytes > tenGB {
		t.Fatalf("MAX_UPLOAD_BYTES default = %d, want a positive value no larger than %d", cfg.MaxUploadBytes, tenGB)
	}
}
