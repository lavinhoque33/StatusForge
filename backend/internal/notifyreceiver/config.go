// Package notifyreceiver implements a loopback-only development fixture for
// inspecting local notification delivery and simulating receiver failures.
// It has no authentication and must never be exposed beyond loopback.
package notifyreceiver

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/netguard"
)

const (
	EnvAddr         = "STATUSFORGE_RECEIVER_ADDR"
	DefaultAddr     = "127.0.0.1:8091"
	DefaultDelay    = 10 * time.Second
	MaxDelay        = 60 * time.Second
	ShutdownTimeout = 12 * time.Second
	maxBodyBytes    = 64 << 10
	maxControlBytes = 4 << 10
)

// Config sets the listen address and the timeout-mode delay. TimeoutDelay is
// injectable for tests; only the address is configurable by environment.
type Config struct {
	Addr         string
	TimeoutDelay time.Duration
}

func DefaultConfig() Config {
	return Config{Addr: DefaultAddr, TimeoutDelay: DefaultDelay}
}

func LoadConfig(getenv func(string) (string, bool)) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("notification receiver: LoadConfig requires a getenv function")
	}
	cfg := DefaultConfig()
	if value, ok := getenv(EnvAddr); ok && strings.TrimSpace(value) != "" {
		cfg.Addr = strings.TrimSpace(value)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if err := ValidateAddr(c.Addr); err != nil {
		return err
	}
	if c.TimeoutDelay < 0 || c.TimeoutDelay > MaxDelay {
		return fmt.Errorf(
			"notification receiver delay %s: must be between 0 and %s",
			c.TimeoutDelay,
			MaxDelay,
		)
	}
	return nil
}

// ValidateAddr rejects wildcard, non-loopback and malformed listen addresses.
func ValidateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("notification receiver address %q: want host:port: %w", addr, err)
	}
	if !netguard.IsLoopbackHost(host) {
		return fmt.Errorf("notification receiver address %q: host %q is not loopback", addr, host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf(
			"notification receiver address %q: port %q must be a number between 1 and 65535",
			addr,
			port,
		)
	}
	return nil
}
