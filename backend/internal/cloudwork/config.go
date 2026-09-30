package cloudwork

import (
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
)

// Binary selects which variables are required.
type Binary int

const (
	PlannerBinary Binary = iota
	WorkerBinary
)

// RuntimeAPIVariable is set by the Lambda runtime; its absence means the
// binary is not running in AWS Lambda.
const RuntimeAPIVariable = "AWS_LAMBDA_RUNTIME_API"

// Config is the Lambda binaries' configuration. The local binary never reads it.
type Config struct {
	Table    string
	QueueURL string
	Targets  string
	Monitors string
	LogLevel slog.Level
}

var tableName = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,255}$`)

// LoadConfig reads and validates the variables of one binary. Errors name the
// variable and never echo its value.
func LoadConfig(lookup func(string) (string, bool), binary Binary) (Config, error) {
	var cfg Config
	table, _ := lookup("STATUSFORGE_TABLE")
	if !tableName.MatchString(table) {
		return cfg, fmt.Errorf("STATUSFORGE_TABLE: required, 3–255 characters [A-Za-z0-9_.-]")
	}
	cfg.Table = table
	cfg.Targets, _ = lookup("STATUSFORGE_CLOUD_TARGETS")
	level, ok := lookup("STATUSFORGE_LOG_LEVEL")
	if !ok || level == "" {
		level = "info"
	}
	switch level {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return cfg, fmt.Errorf("STATUSFORGE_LOG_LEVEL: must be debug, info, warn, or error")
	}
	if binary != PlannerBinary {
		return cfg, nil
	}
	queue, _ := lookup("STATUSFORGE_WORK_QUEUE_URL")
	u, err := url.Parse(queue)
	if queue == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return cfg, fmt.Errorf("STATUSFORGE_WORK_QUEUE_URL: required, an https SQS queue URL")
	}
	cfg.QueueURL = queue
	monitors, ok := lookup(MonitorsVariable)
	if !ok {
		return cfg, fmt.Errorf("%s: required (may be [])", MonitorsVariable)
	}
	cfg.Monitors = monitors
	return cfg, nil
}
