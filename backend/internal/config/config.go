package config

import (
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/netguard"
)

type Config struct {
	HTTPAddr                string
	DynamoDBEndpoint        string
	DynamoDBRegion          string
	DynamoDBAccessKeyID     string
	DynamoDBSecretAccessKey string
	LogLevel                string
	LogFormat               string
	ShutdownTimeout         time.Duration
	ReadinessTimeout        time.Duration
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
	}
	if err := ValidateLoopbackAddr("STATUSFORGE_HTTP_ADDR", cfg.HTTPAddr); err != nil {
		return Config{}, err
	}
	if cfg.DynamoDBEndpoint == "" {
		return Config{}, fmt.Errorf("STATUSFORGE_DYNAMODB_ENDPOINT: set the local endpoint (e.g. http://127.0.0.1:8000); there is no hosted fallback")
	}
	u, err := url.Parse(cfg.DynamoDBEndpoint)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || !netguard.IsLoopbackHost(u.Hostname()) || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return Config{}, fmt.Errorf("STATUSFORGE_DYNAMODB_ENDPOINT: must be an http(s) URL with a loopback-only host")
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
