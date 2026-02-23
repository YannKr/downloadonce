package config

import (
	"os"
	"strconv"
)

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
}

func Load() *Config {
	return &Config{
		ListenAddr:          envOr("LISTEN_ADDR", ":8080"),
		DataDir:             envOr("DATA_DIR", "./data"),
		BaseURL:             envOr("BASE_URL", "http://localhost:8080"),
		SessionSecret:       envOr("SESSION_SECRET", "change-me-in-production-32-bytes!"),
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
		CleanupIntervalMins: envIntOr("CLEANUP_INTERVAL_MINS", 60),
		AllowRegistration:   envBoolOr("ALLOW_REGISTRATION", false),
	}
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

func envBoolOr(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
