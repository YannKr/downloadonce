package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// PlaceholderSecrets are values that have been published in this repository as
// example or default SESSION_SECRETs. They sign the session cookie and the CSRF
// token, so a deployment that copies one is open to anyone who has read the
// repository. Add to this list, never remove from it.
var PlaceholderSecrets = []string{
	"change-me-in-production-32-bytes!",
	"change-me-before-going-to-production",
	"change-me",
	"changeme",
	"secret",
}

// IsPlaceholderSecret reports whether s is a known-public value. The comparison
// ignores case and surrounding space so a near-copy is caught too.
func IsPlaceholderSecret(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range PlaceholderSecrets {
		if s == strings.ToLower(p) {
			return true
		}
	}
	// Anything that still says "change me" was never changed. Split on
	// separators so "exchange mechanism" is not mistaken for one.
	words := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i, w := range words {
		if w == "changeme" {
			return true
		}
		if w == "change" && i+1 < len(words) && words[i+1] == "me" {
			return true
		}
	}
	return false
}

// MinSecretLen is the smallest SESSION_SECRET the server accepts.
const MinSecretLen = 32

type Config struct {
	ListenAddr     string
	DataDir        string
	BaseURL        string
	SessionSecret  string
	TrustedProxies []netip.Prefix
	MaxUploadBytes int64
	WorkerCount    int
	FontPath       string
	LogLevel       string
	VenvPath       string
	ScriptsDir     string // set at runtime after extracting embedded scripts

	// SMTP
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	// Cleanup
	CleanupIntervalMins int

	// Registration
	AllowRegistration bool

	// Chunked upload
	UploadSessionTTLHours int

	// Disk space monitoring
	MaxStorageBytes     int64
	WMCompressionFactor float64
	DiskWarnYellowPct   float64
	DiskWarnRedPct      float64
	DiskWarnBlockPct    float64
}

func Load() *Config {
	return &Config{
		ListenAddr:            envOr("LISTEN_ADDR", ":8080"),
		DataDir:               envOr("DATA_DIR", "./data"),
		BaseURL:               envOr("BASE_URL", "http://localhost:8080"),
		SessionSecret:         envOr("SESSION_SECRET", ""),
		TrustedProxies:        parsePrefixes(envOr("TRUSTED_PROXIES", "")),
		MaxUploadBytes:        envInt64Or("MAX_UPLOAD_BYTES", 5*1024*1024*1024),
		WorkerCount:           envIntOr("WORKER_COUNT", 2),
		FontPath:              envOr("FONT_PATH", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"),
		LogLevel:              envOr("LOG_LEVEL", "info"),
		VenvPath:              envOr("VENV_PATH", "/opt/venv"),
		SMTPHost:              envOr("SMTP_HOST", ""),
		SMTPPort:              envIntOr("SMTP_PORT", 587),
		SMTPUser:              envOr("SMTP_USER", ""),
		SMTPPass:              envOr("SMTP_PASS", ""),
		SMTPFrom:              envOr("SMTP_FROM", ""),
		CleanupIntervalMins:   envIntOr("CLEANUP_INTERVAL_MINS", 60),
		AllowRegistration:     envBoolOr("ALLOW_REGISTRATION", false),
		UploadSessionTTLHours: envIntOr("UPLOAD_SESSION_TTL_HOURS", 24),
		MaxStorageBytes:       envInt64Or("MAX_STORAGE_BYTES", 0),
		WMCompressionFactor:   envFloat64Or("WM_COMPRESSION_FACTOR", 0.9),
		DiskWarnYellowPct:     envFloat64Or("DISK_WARN_YELLOW_PCT", 20.0),
		DiskWarnRedPct:        envFloat64Or("DISK_WARN_RED_PCT", 10.0),
		DiskWarnBlockPct:      envFloat64Or("DISK_WARN_BLOCK_PCT", 5.0),
	}
}

// ResolveSessionSecret validates SESSION_SECRET. When the variable is unset it
// reads a secret from <DataDir>/session_secret, generating and persisting a
// random one on first start. It refuses the old placeholder value and any
// secret shorter than MinSecretLen.
func (c *Config) ResolveSessionSecret() error {
	if c.SessionSecret != "" {
		if IsPlaceholderSecret(c.SessionSecret) {
			return errors.New("SESSION_SECRET is a known-public example value: set a random secret or unset it to have one generated")
		}
		if len(c.SessionSecret) < MinSecretLen {
			return fmt.Errorf("SESSION_SECRET must be at least %d bytes", MinSecretLen)
		}
		return nil
	}

	path := filepath.Join(c.DataDir, "session_secret")
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); len(s) >= MinSecretLen && !IsPlaceholderSecret(s) {
			c.SessionSecret = s
			return nil
		}
	}

	buf := make([]byte, MinSecretLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generate session secret: %w", err)
	}
	secret := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		return fmt.Errorf("persist session secret: %w", err)
	}
	c.SessionSecret = secret
	return nil
}

// parsePrefixes reads a comma-separated list of CIDR blocks or bare IPs. A
// bare IP becomes a single-address prefix. Entries that do not parse are
// dropped with a warning, so one typo cannot widen the trusted set.
func parsePrefixes(s string) []netip.Prefix {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(part); err == nil {
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		slog.Warn("TRUSTED_PROXIES: ignoring entry that is not a CIDR or IP", "entry", part)
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envInt64Or(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func envFloat64Or(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
