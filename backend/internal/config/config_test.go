package config

import (
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { v, ok := values[key]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"STATUSFORGE_DYNAMODB_ENDPOINT": "http://127.0.0.1:8000"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.DynamoDBRegion != "local" || cfg.DynamoDBAccessKeyID != "local" || cfg.DynamoDBSecretAccessKey != "local" || cfg.LogLevel != "info" || cfg.LogFormat != "text" || cfg.ShutdownTimeout != 10*time.Second || cfg.ReadinessTimeout != 2*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadAddresses(t *testing.T) {
	for _, tt := range []struct {
		variable, value string
		valid           bool
	}{
		{"STATUSFORGE_HTTP_ADDR", "localhost:8080", true},
		{"STATUSFORGE_HTTP_ADDR", "[::1]:8080", true},
		{"STATUSFORGE_HTTP_ADDR", "127.0.0.2:8080", true},
		{"STATUSFORGE_HTTP_ADDR", ":8080", false},
		{"STATUSFORGE_HTTP_ADDR", "0.0.0.0:8080", false},
		{"STATUSFORGE_HTTP_ADDR", "[::]:8080", false},
		{"STATUSFORGE_HTTP_ADDR", "192.168.0.10:8080", false},
		{"STATUSFORGE_HTTP_ADDR", "example.com:8080", false},
		{"STATUSFORGE_DYNAMODB_ENDPOINT", "http://localhost:8000", true},
		{"STATUSFORGE_DYNAMODB_ENDPOINT", "https://dynamodb.us-east-1.amazonaws.com", false},
		{"STATUSFORGE_DYNAMODB_ENDPOINT", "localhost:8000", false},
		{"STATUSFORGE_DYNAMODB_ENDPOINT", "ftp://127.0.0.1", false},
	} {
		t.Run(tt.variable+"="+tt.value, func(t *testing.T) {
			values := map[string]string{"STATUSFORGE_DYNAMODB_ENDPOINT": "http://127.0.0.1:8000", tt.variable: tt.value}
			_, err := Load(env(values))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, err=%v", tt.valid, err)
			}
			if !tt.valid && (!strings.Contains(err.Error(), tt.variable) || (tt.variable == "STATUSFORGE_HTTP_ADDR" && !strings.Contains(err.Error(), "loopback"))) {
				t.Fatalf("unclear error: %v", err)
			}
		})
	}
}

func TestLoadMissingEndpoint(t *testing.T) {
	_, err := Load(env(nil))
	if err == nil || !strings.Contains(err.Error(), "STATUSFORGE_DYNAMODB_ENDPOINT") || !strings.Contains(err.Error(), "no hosted fallback") || !strings.Contains(err.Error(), "http://127.0.0.1:8000") {
		t.Fatalf("unclear missing endpoint: %v", err)
	}
}

func TestLoadInvalidSettings(t *testing.T) {
	for _, tt := range []struct{ key, value string }{
		{"STATUSFORGE_LOG_LEVEL", "trace"}, {"STATUSFORGE_LOG_FORMAT", "xml"},
		{"STATUSFORGE_SHUTDOWN_TIMEOUT", "oops"}, {"STATUSFORGE_SHUTDOWN_TIMEOUT", "0s"},
		{"STATUSFORGE_READINESS_TIMEOUT", "oops"}, {"STATUSFORGE_READINESS_TIMEOUT", "-1s"},
	} {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			_, err := Load(env(map[string]string{"STATUSFORGE_DYNAMODB_ENDPOINT": "http://127.0.0.1:8000", tt.key: tt.value}))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("expected %s error: %v", tt.key, err)
			}
		})
	}
}
