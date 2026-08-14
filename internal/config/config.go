package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PlaceholderSecret is the value that earlier releases used as the default
// SESSION_SECRET. It signs the session cookie, so it must never be accepted.
const PlaceholderSecret = "change-me-in-production-32-bytes!"

// MinSecretLen is the smallest SESSION_SECRET the server accepts.
const MinSecretLen = 32

type Config struct {
	ListenAddr     string
	DataDir        string
	BaseURL        string
	SessionSecret  string
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
	MaxStorageBytes    int64
	WMCompressionFactor float64
	DiskWarnYellowPct  float64
	DiskWarnRedPct     float64
	DiskWarnBlockPct   float64
}

func Load() *Config {
	return &Config{
		ListenAddr:          envOr("LISTEN_ADDR", ":8080"),
		DataDir:             envOr("DATA_DIR", "./data"),
		BaseURL:             envOr("BASE_URL", "http://localhost:8080"),
		SessionSecret:       envOr("SESSION_SECRET", ""),
		MaxUploadBytes:      envInt64Or("MAX_UPLOAD_BYTES", 50*1024*1024*1024),
		WorkerCount:         envIntOr("WORKER_COUNT", 2),
		FontPath:            envOr("FONT_PATH", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"),
		LogLevel:            envOr("LOG_LEVEL", "info"),
		VenvPath:            envOr("VENV_PATH", "/opt/venv"),
		SMTPHost:            envOr("SMTP_HOST", ""),
		SMTPPort:            envIntOr("SMTP_PORT", 587),
		SMTPUser:            envOr("SMTP_USER", ""),
		SMTPPass:            envOr("SMTP_PASS", ""),
		SMTPFrom:            envOr("SMTP_FROM", ""),
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
		if c.SessionSecret == PlaceholderSecret {
			return errors.New("SESSION_SECRET is the placeholder value: set a random secret or unset it to have one generated")
		}
		if len(c.SessionSecret) < MinSecretLen {
			return fmt.Errorf("SESSION_SECRET must be at least %d bytes", MinSecretLen)
		}
		return nil
	}

	path := filepath.Join(c.DataDir, "session_secret")
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); len(s) >= MinSecretLen && s != PlaceholderSecret {
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
