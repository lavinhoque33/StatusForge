// Package samplejob implements StatusForge's sample heartbeat job fixture: a
// small scheduled job that reports to a heartbeat ingest endpoint so local
// development and drills can exercise heartbeats without a real backup or cron
// job.
//
// # Development-only: no authentication, never expose
//
// The fixture's control routes are unauthenticated: anyone who can reach them
// can read its state, change the reporting mode, and force reports. It
// therefore refuses to start unless the configured listen host is loopback
// (the literal host "localhost", an address in 127.0.0.0/8, or ::1), and there
// is deliberately no configuration switch that weakens that check. Never bind
// it to a non-loopback interface, never port-forward it, and never deploy it.
//
// # Reporting
//
// Every interval (Config.Interval) the fixture sends one report to
// Config.ReportURL as an HTTP POST carrying "Authorization: Bearer <token>".
// The report URL must be http and loopback, and both the URL and the token
// must be set before any report is sent; a fixture without them still starts
// and its control routes still work. The plaintext token is never written to a
// log, an error message, or a response: the fixture records only whether it is
// set. The report client has a fixed deadline, follows no redirects, and never
// uses a proxy.
//
// # Routes
//
// The route set is fixed:
//
//	GET  /status          current mode and report counters; carries no token
//	GET  /control/mode    {"mode": ..., "lateSeconds": ...}
//	PUT  /control/mode    {"mode":"normal"|"skip"|"fail"|"late"|"stop",
//	                      "lateSeconds":N}
//	POST /control/run     report once now with the current mode (stop: 409)
//	POST /control/replay  resend the last report verbatim (duplicate runId)
//
//	POST /control/deploy  post a deployment marker with the application token
package samplejob

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvAddr, EnvReportURL, EnvToken, and EnvIntervalSeconds name the
	// environment variables LoadConfig reads.
	EnvAddr            = "STATUSFORGE_SAMPLE_JOB_ADDR"
	EnvReportURL       = "STATUSFORGE_SAMPLE_JOB_REPORT_URL"
	EnvToken           = "STATUSFORGE_SAMPLE_JOB_TOKEN"
	EnvIntervalSeconds = "STATUSFORGE_SAMPLE_JOB_INTERVAL_SECONDS"
	EnvDeployURL       = "STATUSFORGE_SAMPLE_JOB_DEPLOY_URL"
	EnvDeployToken     = "STATUSFORGE_SAMPLE_JOB_DEPLOY_TOKEN"

	// DefaultAddr is the fixture's loopback-only default listen address.
	DefaultAddr = "127.0.0.1:8092"

	// DefaultIntervalSeconds is the default delay between scheduled reports.
	DefaultIntervalSeconds = 60

	// DefaultLateSeconds is the default delay the late mode adds to a tick.
	DefaultLateSeconds = 5

	// MinIntervalSeconds and MaxIntervalSeconds bound the report interval.
	MinIntervalSeconds = 1
	MaxIntervalSeconds = 86400

	// MaxLateSeconds bounds the late mode's delay.
	MaxLateSeconds = 86400

	// ReportTimeout is the deadline of one report request, including
	// connection, request, and response.
	ReportTimeout = 5 * time.Second

	// ShutdownTimeout is how long the process waits for a graceful shutdown.
	ShutdownTimeout = 10 * time.Second

	// timestampLayout is the RFC 3339 UTC millisecond form used on the wire.
	timestampLayout = "2006-01-02T15:04:05.000Z"

	maxControlBytes  = 4 << 10
	maxResponseBytes = 4 << 10
)

// Config is the fixture's startup configuration. A fixture without a report
// URL or token starts normally but sends no report until both are set; see
// Reporting.
type Config struct {
	Addr        string
	ReportURL   string
	Token       string
	DeployURL   string
	DeployToken string
	Interval    time.Duration

	// Now and NewTicker are injectable for tests; New substitutes the real
	// clock and ticker when they are nil.
	Now       func() time.Time
	NewTicker func(time.Duration) Ticker
}

// DefaultConfig returns the contract defaults: 127.0.0.1:8092, a 60 s
// interval, no reporting target, and the real clock.
func DefaultConfig() Config {
	return Config{
		Addr:      DefaultAddr,
		Interval:  DefaultIntervalSeconds * time.Second,
		Now:       time.Now,
		NewTicker: newRealTicker,
	}
}

// Reporting reports whether the fixture is allowed to send reports: a report
// needs both a target URL and a token.
func (c Config) Reporting() bool {
	return c.ReportURL != "" && c.Token != ""
}

// LoadConfig resolves the fixture configuration from getenv (normally
// os.LookupEnv). Unset or blank variables fall back to DefaultConfig, and the
// result is validated before it is returned, so a configuration that could
// bind beyond loopback, or report to a non-loopback or non-http URL, is always
// rejected.
func LoadConfig(getenv func(string) (string, bool)) (Config, error) {
	if getenv == nil {
		return Config{}, errors.New("sample job: LoadConfig requires a getenv function")
	}
	cfg := DefaultConfig()
	if value, ok := getenv(EnvAddr); ok && strings.TrimSpace(value) != "" {
		cfg.Addr = strings.TrimSpace(value)
	}
	if value, ok := getenv(EnvReportURL); ok && strings.TrimSpace(value) != "" {
		cfg.ReportURL = strings.TrimSpace(value)
	}
	// The token is opaque; only surrounding whitespace is dropped, because a
	// token copied from a terminal or an .env file can carry a stray newline.
	if value, ok := getenv(EnvToken); ok && strings.TrimSpace(value) != "" {
		cfg.Token = strings.TrimSpace(value)
	}
	if value, ok := getenv(EnvDeployURL); ok && strings.TrimSpace(value) != "" {
		cfg.DeployURL = strings.TrimSpace(value)
	}
	if value, ok := getenv(EnvDeployToken); ok && strings.TrimSpace(value) != "" {
		cfg.DeployToken = strings.TrimSpace(value)
	}
	if value, ok := getenv(EnvIntervalSeconds); ok && strings.TrimSpace(value) != "" {
		seconds, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("sample job interval %q: must be an integer", value)
		}
		cfg.Interval = time.Duration(seconds) * time.Second
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
	if c.ReportURL != "" {
		if err := ValidateReportURL(c.ReportURL); err != nil {
			return err
		}
	}
	if c.DeployURL != "" {
		if err := ValidateReportURL(c.DeployURL); err != nil {
			return err
		}
	}
	intervalSeconds := int(c.Interval / time.Second)
	if c.Interval%time.Second != 0 || intervalSeconds < MinIntervalSeconds ||
		intervalSeconds > MaxIntervalSeconds {
		return fmt.Errorf(
			"sample job interval %s: must be a whole number of seconds between %d and %d",
			c.Interval,
			MinIntervalSeconds,
			MaxIntervalSeconds,
		)
	}
	return nil
}

// ValidateAddr requires a host:port listen address whose host is loopback.
// Hostnames other than "localhost" are rejected rather than resolved, so DNS
// cannot make a validated address point somewhere else at listen time.
func ValidateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("sample job address: want host:port: %w", err)
	}
	if !isLoopbackHost(host) {
		return errors.New("sample job address: host is not loopback")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("sample job address: port must be a number between 1 and 65535")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateReportURL requires an http URL on a loopback host with no userinfo
// and no fragment. Hostnames other than "localhost" are rejected rather than
// resolved, so a validated URL cannot be redirected elsewhere by DNS.
func ValidateReportURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("sample job report URL: invalid URL")
	}
	if parsed.Scheme != "http" {
		return errors.New("sample job report URL: scheme must be http")
	}
	if parsed.User != nil {
		return errors.New("sample job report URL: must not contain user info")
	}
	if parsed.Hostname() == "" {
		return errors.New("sample job report URL: missing host")
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return errors.New("sample job report URL: host is not loopback")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("sample job report URL: port must be a number between 1 and 65535")
		}
	}
	if parsed.Fragment != "" {
		return errors.New("sample job report URL: must not contain a fragment")
	}
	return nil
}
