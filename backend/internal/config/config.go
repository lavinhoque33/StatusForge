package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/netguard"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

type Config struct {
	HTTPAddr                    string
	DynamoDBEndpoint            string
	DynamoDBRegion              string
	DynamoDBAccessKeyID         string
	DynamoDBSecretAccessKey     string
	LogLevel                    string
	LogFormat                   string
	ShutdownTimeout             time.Duration
	ReadinessTimeout            time.Duration
	AllowedTargets              string
	DynamoDBTable               string
	Workers                     int
	MinIntervalSeconds          int
	SchedulerEnabled            bool
	NotifyURL                   string
	DeliveryWorkers             int
	DeliveryRetrySchedule       []time.Duration
	ReminderIntervalSeconds     int
	LivenessIntervalSeconds     int
	HousekeepingIntervalSeconds int
}

func Load(lookup func(string) (string, bool)) (Config, error) {
	get := func(key, fallback string) string {
		if value, ok := lookup(key); ok {
			return value
		}
		return fallback
	}
	cfg := Config{
		HTTPAddr:                get("STATUSFORGE_HTTP_ADDR", "127.0.0.1:8080"),
		DynamoDBEndpoint:        get("STATUSFORGE_DYNAMODB_ENDPOINT", ""),
		DynamoDBRegion:          get("STATUSFORGE_DYNAMODB_REGION", "local"),
		DynamoDBAccessKeyID:     get("STATUSFORGE_DYNAMODB_ACCESS_KEY_ID", "local"),
		DynamoDBSecretAccessKey: get("STATUSFORGE_DYNAMODB_SECRET_ACCESS_KEY", "local"),
		LogLevel:                get("STATUSFORGE_LOG_LEVEL", "info"),
		LogFormat:               get("STATUSFORGE_LOG_FORMAT", "text"),
		AllowedTargets:          get("STATUSFORGE_ALLOWED_TARGETS", "127.0.0.1:8090"),
		DynamoDBTable:           get("STATUSFORGE_DYNAMODB_TABLE", "statusforge"),
		Workers:                 4,
		MinIntervalSeconds:      60,
		SchedulerEnabled:        true,
	}
	cfg.NotifyURL = get("STATUSFORGE_NOTIFY_URL", "http://127.0.0.1:8091/notify")
	uNotify, err := url.Parse(cfg.NotifyURL)
	if err != nil || uNotify == nil {
		return Config{}, fmt.Errorf("STATUSFORGE_NOTIFY_URL: invalid URL")
	}
	allow := net.JoinHostPort(uNotify.Hostname(), uNotify.Port())
	if uNotify.Port() == "" {
		allow = net.JoinHostPort(uNotify.Hostname(), "80")
	}
	policy, err := targetpolicy.Parse(allow)
	if err != nil {
		return Config{}, fmt.Errorf("STATUSFORGE_NOTIFY_URL: %w", err)
	}
	if code, _ := policy.Validate(cfg.NotifyURL); code != "" {
		return Config{}, fmt.Errorf("STATUSFORGE_NOTIFY_URL: %s", code)
	}
	cfg.DeliveryWorkers, err = strconv.Atoi(get("STATUSFORGE_DELIVERY_WORKERS", "1"))
	if err != nil || cfg.DeliveryWorkers < 1 || cfg.DeliveryWorkers > 4 {
		return Config{}, fmt.Errorf("STATUSFORGE_DELIVERY_WORKERS: must be an integer 1–4")
	}
	delays := strings.Split(
		get("STATUSFORGE_DELIVERY_RETRY_SCHEDULE", "10s,30s,90s,5m,15m,30m"),
		",",
	)
	if len(delays) < 1 || len(delays) > 10 {
		return Config{}, fmt.Errorf("STATUSFORGE_DELIVERY_RETRY_SCHEDULE: must contain 1–10 delays")
	}
	for _, raw := range delays {
		d, parseErr := time.ParseDuration(raw)
		if parseErr != nil || d < time.Second || d > time.Hour {
			return Config{}, fmt.Errorf(
				"STATUSFORGE_DELIVERY_RETRY_SCHEDULE: each delay must be 1s–1h",
			)
		}
		cfg.DeliveryRetrySchedule = append(cfg.DeliveryRetrySchedule, d)
	}
	cfg.ReminderIntervalSeconds, err = strconv.Atoi(
		get("STATUSFORGE_REMINDER_INTERVAL_SECONDS", "21600"),
	)
	if err != nil || cfg.ReminderIntervalSeconds < 60 || cfg.ReminderIntervalSeconds > 86400 {
		return Config{}, fmt.Errorf(
			"STATUSFORGE_REMINDER_INTERVAL_SECONDS: must be an integer 60–86400",
		)
	}
	cfg.LivenessIntervalSeconds, err = strconv.Atoi(
		get("STATUSFORGE_LIVENESS_INTERVAL_SECONDS", "10"),
	)
	if err != nil || cfg.LivenessIntervalSeconds < 2 || cfg.LivenessIntervalSeconds > 60 {
		return Config{}, fmt.Errorf(
			"STATUSFORGE_LIVENESS_INTERVAL_SECONDS: must be an integer 2–60",
		)
	}
	cfg.HousekeepingIntervalSeconds, err = strconv.Atoi(
		get("STATUSFORGE_HOUSEKEEPING_INTERVAL_SECONDS", "60"),
	)
	if err != nil || cfg.HousekeepingIntervalSeconds < 2 || cfg.HousekeepingIntervalSeconds > 3600 {
		return Config{}, fmt.Errorf(
			"STATUSFORGE_HOUSEKEEPING_INTERVAL_SECONDS: must be an integer 2–3600",
		)
	}
	if err := validateTargets(cfg.AllowedTargets); err != nil {
		return Config{}, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{3,255}$`).MatchString(cfg.DynamoDBTable) {
		return Config{}, fmt.Errorf(
			"STATUSFORGE_DYNAMODB_TABLE: must be 3–255 characters [A-Za-z0-9_.-]",
		)
	}
	if err := ValidateLoopbackAddr("STATUSFORGE_HTTP_ADDR", cfg.HTTPAddr); err != nil {
		return Config{}, err
	}
	if cfg.DynamoDBEndpoint == "" {
		return Config{}, fmt.Errorf(
			"STATUSFORGE_DYNAMODB_ENDPOINT: set the local endpoint (e.g. http://127.0.0.1:8000); there is no hosted fallback",
		)
	}
	u, err := url.Parse(cfg.DynamoDBEndpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") ||
		!netguard.IsLoopbackHost(u.Hostname()) ||
		u.User != nil ||
		u.Opaque != "" ||
		u.RawQuery != "" ||
		u.Fragment != "" {
		return Config{}, fmt.Errorf(
			"STATUSFORGE_DYNAMODB_ENDPOINT: must be an http(s) URL with a loopback-only host",
		)
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("STATUSFORGE_LOG_LEVEL: must be debug, info, warn, or error")
	}
	switch cfg.LogFormat {
	case "text", "json":
	default:
		return Config{}, fmt.Errorf("STATUSFORGE_LOG_FORMAT: must be text or json")
	}
	cfg.ShutdownTimeout, err = time.ParseDuration(get("STATUSFORGE_SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || cfg.ShutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("STATUSFORGE_SHUTDOWN_TIMEOUT: must be a positive duration")
	}
	cfg.ReadinessTimeout, err = time.ParseDuration(get("STATUSFORGE_READINESS_TIMEOUT", "2s"))
	if err != nil || cfg.ReadinessTimeout <= 0 {
		return Config{}, fmt.Errorf("STATUSFORGE_READINESS_TIMEOUT: must be a positive duration")
	}
	cfg.Workers, err = strconv.Atoi(get("STATUSFORGE_WORKERS", "4"))
	if err != nil || cfg.Workers < 1 || cfg.Workers > 16 {
		return Config{}, fmt.Errorf("STATUSFORGE_WORKERS: must be an integer 1–16")
	}
	cfg.MinIntervalSeconds, err = strconv.Atoi(get("STATUSFORGE_MIN_INTERVAL_SECONDS", "60"))
	if err != nil || (cfg.MinIntervalSeconds != 10 && cfg.MinIntervalSeconds != 15 &&
		cfg.MinIntervalSeconds != 30 && cfg.MinIntervalSeconds != 60) {
		return Config{}, fmt.Errorf("STATUSFORGE_MIN_INTERVAL_SECONDS: must be 10, 15, 30, or 60")
	}
	cfg.SchedulerEnabled, err = strconv.ParseBool(get("STATUSFORGE_SCHEDULER_ENABLED", "true"))
	if err != nil || (get("STATUSFORGE_SCHEDULER_ENABLED", "true") != "true" &&
		get("STATUSFORGE_SCHEDULER_ENABLED", "true") != "false") {
		return Config{}, fmt.Errorf("STATUSFORGE_SCHEDULER_ENABLED: must be true or false")
	}
	return cfg, nil
}

// ValidateLoopbackAddr rejects wildcard listeners and hostnames requiring DNS.
func ValidateLoopbackAddr(variable, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !netguard.IsLoopbackHost(host) {
		return fmt.Errorf("%s: address must have a loopback-only host and port", variable)
	}
	return nil
}

func validateTargets(raw string) error {
	if raw == "" {
		return fmt.Errorf("STATUSFORGE_ALLOWED_TARGETS: list must not be empty")
	}
	for _, entry := range strings.Split(raw, ",") {
		host, port, err := net.SplitHostPort(strings.TrimSpace(entry))
		if err != nil || !netguard.IsLoopbackHost(host) {
			return fmt.Errorf(
				"STATUSFORGE_ALLOWED_TARGETS: each entry must be a loopback host:port",
			)
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("STATUSFORGE_ALLOWED_TARGETS: port must be 1–65535")
		}
	}
	return nil
}

// ValidateTable applies the same local table-name restriction to CLI overrides.
func ValidateTable(name string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{3,255}$`).MatchString(name) {
		return fmt.Errorf("table: must be 3–255 characters [A-Za-z0-9_.-]")
	}
	return nil
}
