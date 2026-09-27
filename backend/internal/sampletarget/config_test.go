package sampletarget

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name: "defaults when nothing is set",
			env:  nil,
			want: Config{Addr: "127.0.0.1:8090", SlowDelay: 5 * time.Second},
		},
		{
			name: "explicit values",
			env: map[string]string{
				EnvAddr:      "127.0.0.1:9123",
				EnvSlowDelay: "1500ms",
			},
			want: Config{Addr: "127.0.0.1:9123", SlowDelay: 1500 * time.Millisecond},
		},
		{
			name: "blank values fall back to defaults",
			env: map[string]string{
				EnvAddr:      "   ",
				EnvSlowDelay: "",
			},
			want: DefaultConfig(),
		},
		{
			name: "localhost is accepted",
			env:  map[string]string{EnvAddr: "localhost:8090"},
			want: Config{Addr: "localhost:8090", SlowDelay: DefaultSlowDelay},
		},
		{
			name: "ipv6 loopback is accepted",
			env:  map[string]string{EnvAddr: "[::1]:8090"},
			want: Config{Addr: "[::1]:8090", SlowDelay: DefaultSlowDelay},
		},
		{
			name: "127/8 addresses are accepted",
			env:  map[string]string{EnvAddr: "127.9.9.9:8090"},
			want: Config{Addr: "127.9.9.9:8090", SlowDelay: DefaultSlowDelay},
		},
		{
			name: "zero delay is accepted",
			env:  map[string]string{EnvSlowDelay: "0s"},
			want: Config{Addr: DefaultAddr, SlowDelay: 0},
		},
		{
			name: "maximum delay is accepted",
			env:  map[string]string{EnvSlowDelay: "60s"},
			want: Config{Addr: DefaultAddr, SlowDelay: MaxSlowDelay},
		},
		{
			name:    "wildcard host is rejected",
			env:     map[string]string{EnvAddr: "0.0.0.0:8090"},
			wantErr: "not loopback",
		},
		{
			name:    "all-interfaces address is rejected",
			env:     map[string]string{EnvAddr: ":8090"},
			wantErr: "missing host",
		},
		{
			name:    "private lan host is rejected",
			env:     map[string]string{EnvAddr: "192.168.0.10:8090"},
			wantErr: "not loopback",
		},
		{
			name:    "non-loopback hostname is rejected",
			env:     map[string]string{EnvAddr: "example.com:8090"},
			wantErr: "not loopback",
		},
		{
			name:    "missing port is rejected",
			env:     map[string]string{EnvAddr: "127.0.0.1"},
			wantErr: "host:port",
		},
		{
			name:    "service-name port is rejected",
			env:     map[string]string{EnvAddr: "127.0.0.1:http"},
			wantErr: "port",
		},
		{
			name:    "out-of-range port is rejected",
			env:     map[string]string{EnvAddr: "127.0.0.1:65536"},
			wantErr: "port",
		},
		{
			name:    "zero port is rejected",
			env:     map[string]string{EnvAddr: "127.0.0.1:0"},
			wantErr: "port",
		},
		{
			name:    "unparsable delay is rejected",
			env:     map[string]string{EnvSlowDelay: "5 seconds"},
			wantErr: "invalid duration",
		},
		{
			name:    "negative delay is rejected",
			env:     map[string]string{EnvSlowDelay: "-1s"},
			wantErr: "negative",
		},
		{
			name:    "delay above maximum is rejected",
			env:     map[string]string{EnvSlowDelay: "61s"},
			wantErr: "exceeds maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadConfig(func(key string) (string, bool) {
				v, ok := tt.env[key]
				return v, ok
			})

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("LoadConfig() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig() error = %q, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() unexpected error: %v", err)
			}
			if cfg != tt.want {
				t.Fatalf("LoadConfig() = %+v, want %+v", cfg, tt.want)
			}
		})
	}
}

func TestLoadConfigRequiresGetenv(t *testing.T) {
	if _, err := LoadConfig(nil); err == nil {
		t.Fatal("LoadConfig(nil) error = nil, want error")
	}
}

func TestNewRejectsUnsafeConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "non-loopback host",
			cfg:     Config{Addr: "0.0.0.0:8090", SlowDelay: time.Second},
			wantErr: "not loopback",
		},
		{
			name:    "all-interfaces address",
			cfg:     Config{Addr: ":8090", SlowDelay: time.Second},
			wantErr: "missing host",
		},
		{
			name:    "missing port",
			cfg:     Config{Addr: "127.0.0.1", SlowDelay: time.Second},
			wantErr: "host:port",
		},
		{
			name:    "negative slow delay",
			cfg:     Config{Addr: DefaultAddr, SlowDelay: -time.Second},
			wantErr: "negative",
		},
		{
			name:    "slow delay above maximum",
			cfg:     Config{Addr: DefaultAddr, SlowDelay: MaxSlowDelay + time.Second},
			wantErr: "exceeds maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Fatalf("New(%+v) error = nil, want error containing %q", tt.cfg, tt.wantErr)
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New(%+v) error = %q, want error containing %q", tt.cfg, err, tt.wantErr)
			}
		})
	}
}

func TestSetModeRejectsInvalidValuesWithoutChangingState(t *testing.T) {
	s := newFixture(t, DefaultSlowDelay)

	for _, invalid := range []Mode{"", "bogus", "Healthy", "ok"} {
		if err := s.SetMode(invalid); err == nil {
			t.Errorf("SetMode(%q) error = nil, want error", invalid)
		}
	}
	if got := s.Mode(); got != ModeHealthy {
		t.Errorf("Mode() = %q after rejected SetMode calls, want %q", got, ModeHealthy)
	}

	for _, valid := range []Mode{ModeFailing, ModeSlow, ModeHealthy} {
		if err := s.SetMode(valid); err != nil {
			t.Errorf("SetMode(%q) unexpected error: %v", valid, err)
		}
		if got := s.Mode(); got != valid {
			t.Errorf("Mode() = %q after SetMode(%q), want %q", got, valid, valid)
		}
	}
}
