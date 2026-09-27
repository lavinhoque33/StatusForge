// Package sampletarget implements StatusForge's controlled sample target
// fixture: a small HTTP server that stands in for a real monitored service
// during local development, demos, and tests.
//
// # Development-only: no authentication, never expose
//
// The fixture deliberately has no authentication, no authorization, no TLS,
// and no rate limiting. Anyone who can reach it can read its state and change
// its simulated behavior through the control routes. It therefore refuses to
// start unless the configured listen host is loopback (the literal host
// "localhost", an address in 127.0.0.0/8, or ::1), and there is deliberately
// no configuration switch that weakens that check. Never bind it to a
// non-loopback interface, never port-forward it, and never deploy it.
//
// # Routes
//
// The route set is fixed:
//
//	GET  /              follows the current mode: 200 "ok", 500 "simulated
//	                    failure", or, after the slow delay, 200 "ok (slow)"
//	GET  /healthy       fixed 200 "ok"
//	GET  /failing       fixed 500 "simulated failure"
//	GET  /slow          fixed delayed 200 "ok (slow)"
//	GET  /control/mode  current mode as JSON
//	PUT  /control/mode  JSON control: {"mode":"healthy"|"failing"|"slow"}
//
// The slow delay defaults to five seconds (DefaultSlowDelay) and is capped at
// MaxSlowDelay; it is configured with EnvSlowDelay at startup.
//
// Every response, including errors, carries the header
// "X-StatusForge-Fixture: sample-target" (see FixtureHeader) so a caller can
// confirm it reached the fixture instead of a real service.
package sampletarget

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
	// FixtureName identifies the fixture in responses and logs.
	FixtureName = "sample-target"

	// FixtureHeader is set on every response the fixture serves. Its value is
	// always FixtureName.
	FixtureHeader = "X-StatusForge-Fixture"

	// EnvAddr and EnvSlowDelay name the environment variables LoadConfig reads.
	EnvAddr      = "STATUSFORGE_SAMPLE_TARGET_ADDR"
	EnvSlowDelay = "STATUSFORGE_SAMPLE_TARGET_SLOW_DELAY"

	// DefaultAddr is the fixture's loopback-only default listen address.
	DefaultAddr = "127.0.0.1:8090"

	// DefaultSlowDelay is the default artificial delay for the slow behavior.
	DefaultSlowDelay = 5 * time.Second

	// MaxSlowDelay caps the artificial delay for both startup configuration and
	// the runtime control route.
	MaxSlowDelay = 60 * time.Second

	// ReadHeaderTimeout bounds reading request headers.
	ReadHeaderTimeout = 5 * time.Second

	// ReadTimeout bounds reading an entire request.
	ReadTimeout = 10 * time.Second

	// WriteTimeout bounds writing a response. It intentionally exceeds
	// MaxSlowDelay so a slow response can still be delivered.
	WriteTimeout = MaxSlowDelay + 15*time.Second

	// IdleTimeout bounds how long an idle keep-alive connection is kept.
	IdleTimeout = 60 * time.Second

	// ShutdownTimeout bounds graceful shutdown once a termination signal is
	// received.
	ShutdownTimeout = 10 * time.Second

	// maxControlBodyBytes bounds control-request bodies, which are tiny JSON
	// documents.
	maxControlBodyBytes = 4 << 10
)

// Config is the fixture's startup configuration.
type Config struct {
	// Addr is the listen address. Its host must be loopback; the fixture has
	// no authentication and must never be exposed.
	Addr string
	// SlowDelay is the artificial delay used by GET /slow and by GET /ok while
	// the fixture is in slow mode. It must be between zero and MaxSlowDelay.
	SlowDelay time.Duration
}

// DefaultConfig returns the fixture's default configuration: 127.0.0.1:8090
// with a five-second slow delay.
func DefaultConfig() Config {
	return Config{Addr: DefaultAddr, SlowDelay: DefaultSlowDelay}
}

// LoadConfig resolves the fixture configuration from getenv (normally
// os.LookupEnv). Unset or blank variables fall back to DefaultConfig, and the
// result is validated before it is returned, so a configuration that could
// bind beyond loopback is always rejected.
func LoadConfig(getenv func(string) (string, bool)) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("sample target: LoadConfig requires a getenv function")
	}

	cfg := DefaultConfig()
	if v, ok := getenv(EnvAddr); ok && strings.TrimSpace(v) != "" {
		cfg.Addr = strings.TrimSpace(v)
	}
	if v, ok := getenv(EnvSlowDelay); ok && strings.TrimSpace(v) != "" {
		delay, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return Config{}, fmt.Errorf("%s: invalid duration %q: %w", EnvSlowDelay, v, err)
		}
		cfg.SlowDelay = delay
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports whether the configuration is safe and usable.
func (c Config) Validate() error {
	if err := ValidateAddr(c.Addr); err != nil {
		return err
	}
	return ValidateSlowDelay(c.SlowDelay)
}

// ValidateAddr requires a host:port listen address whose host is loopback.
// Hostnames other than "localhost" are rejected rather than resolved, so DNS
// cannot make a validated address point somewhere else at listen time.
func ValidateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("sample target address %q: want host:port such as %q: %w", addr, DefaultAddr, err)
	}
	if host == "" {
		return fmt.Errorf("sample target address %q: missing host; the fixture has no authentication and must never listen on every interface", addr)
	}
	if !netguard.IsLoopbackHost(host) {
		return fmt.Errorf("sample target address %q: host %q is not loopback; the fixture has no authentication and must never be exposed", addr, host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("sample target address %q: port %q must be a number between 1 and 65535", addr, port)
	}
	return nil
}

// ValidateSlowDelay accepts delays from zero through MaxSlowDelay.
func ValidateSlowDelay(d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("sample target slow delay %s: must not be negative", d)
	}
	if d > MaxSlowDelay {
		return fmt.Errorf("sample target slow delay %s: exceeds maximum %s", d, MaxSlowDelay)
	}
	return nil
}
