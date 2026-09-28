package samplejob

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.Interval != DefaultIntervalSeconds*time.Second {
		t.Errorf("Interval = %s, want %ds", cfg.Interval, DefaultIntervalSeconds)
	}
	if cfg.ReportURL != "" || cfg.Token != "" {
		t.Errorf("ReportURL = %q, Token = %q, want both empty", cfg.ReportURL, cfg.Token)
	}
	if cfg.Reporting() {
		t.Error("Reporting() = true without a report URL and token, want false")
	}
	if cfg.Now == nil || cfg.NewTicker == nil {
		t.Error("DefaultConfig must supply the real clock and ticker")
	}
}

func TestLoadConfigReadsEveryVariable(t *testing.T) {
	env := map[string]string{
		EnvAddr:            "localhost:18092",
		EnvReportURL:       "http://[::1]:8080/ingest/heartbeats/abc",
		EnvToken:           "  sfh_example-token\n",
		EnvIntervalSeconds: "15",
	}
	cfg, err := LoadConfig(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Addr != "localhost:18092" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.ReportURL != "http://[::1]:8080/ingest/heartbeats/abc" {
		t.Errorf("ReportURL = %q", cfg.ReportURL)
	}
	if cfg.Token != "sfh_example-token" {
		t.Errorf("Token = %q, want the token without surrounding whitespace", cfg.Token)
	}
	if cfg.Interval != 15*time.Second {
		t.Errorf("Interval = %s, want 15s", cfg.Interval)
	}
	if !cfg.Reporting() {
		t.Error("Reporting() = false with a URL and token set, want true")
	}
}

func TestLoadConfigRejectsUnsafeOrInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "wildcard listen address",
			env:     map[string]string{EnvAddr: "0.0.0.0:8092"},
			wantErr: "not loopback",
		},
		{
			name:    "listen address without a host",
			env:     map[string]string{EnvAddr: ":8092"},
			wantErr: "not loopback",
		},
		{
			name:    "listen address without a port",
			env:     map[string]string{EnvAddr: "127.0.0.1"},
			wantErr: "host:port",
		},
		{
			name:    "listen port zero",
			env:     map[string]string{EnvAddr: "127.0.0.1:0"},
			wantErr: "port",
		},
		{
			name:    "https report URL",
			env:     map[string]string{EnvReportURL: "https://127.0.0.1:8080/ingest/heartbeats/x"},
			wantErr: "scheme must be http",
		},
		{
			name: "report URL with a private host",
			env: map[string]string{
				EnvReportURL: "http://192.168.1.10:8080/ingest/heartbeats/x",
			},
			wantErr: "not loopback",
		},
		{
			name:    "report URL with a hostname",
			env:     map[string]string{EnvReportURL: "http://example.com:8080/ingest/heartbeats/x"},
			wantErr: "not loopback",
		},
		{
			name: "report URL with user info",
			env: map[string]string{
				EnvReportURL: "http://user:pass@127.0.0.1:8080/ingest/heartbeats/x",
			},
			wantErr: "user info",
		},
		{
			name:    "report URL with a fragment",
			env:     map[string]string{EnvReportURL: "http://127.0.0.1:8080/x#frag"},
			wantErr: "fragment",
		},
		{
			name:    "interval zero",
			env:     map[string]string{EnvIntervalSeconds: "0"},
			wantErr: "between 1 and 86400",
		},
		{
			name:    "interval above the maximum",
			env:     map[string]string{EnvIntervalSeconds: "86401"},
			wantErr: "between 1 and 86400",
		},
		{
			name:    "interval is not a number",
			env:     map[string]string{EnvIntervalSeconds: "soon"},
			wantErr: "must be an integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(func(key string) (string, bool) {
				value, ok := tt.env[key]
				return value, ok
			})
			if err == nil {
				t.Fatalf("LoadConfig(%v) error = nil, want %q", tt.env, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf(
					"LoadConfig(%v) error = %v, want it to mention %q",
					tt.env,
					err,
					tt.wantErr,
				)
			}
		})
	}
}

func TestLoadConfigRequiresGetenv(t *testing.T) {
	if _, err := LoadConfig(nil); err == nil {
		t.Fatal("LoadConfig(nil) error = nil, want error")
	}
}

func TestNewRejectsUnsafeConfigAndAcceptsWithoutReportingTarget(t *testing.T) {
	if _, err := New(Config{
		Addr:      DefaultAddr,
		ReportURL: "http://10.1.2.3:8080/ingest/heartbeats/x",
		Token:     "sfh_x",
		Interval:  time.Second,
	}); err == nil {
		t.Fatal("New() error = nil for a non-loopback report URL, want error")
	}
	if _, err := New(Config{Addr: "0.0.0.0:8092", Interval: time.Second}); err == nil {
		t.Fatal("New() error = nil for a wildcard address, want error")
	}

	// A fixture without a reporting target is usable for control-route drills:
	// it starts, keeps the contract defaults, and sends nothing.
	fixture, err := New(Config{Addr: DefaultAddr, Interval: time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := fixture.Mode(); got != ModeNormal {
		t.Errorf("Mode() = %q, want %q", got, ModeNormal)
	}
	if got := fixture.LateSeconds(); got != DefaultLateSeconds {
		t.Errorf("LateSeconds() = %d, want %d", got, DefaultLateSeconds)
	}
}
