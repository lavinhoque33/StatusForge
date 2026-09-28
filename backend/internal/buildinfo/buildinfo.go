// Package buildinfo reports the executable's version without environment configuration.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is set with -ldflags -X at release build time.
var Version string

func Current() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return "dev"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if strings.EqualFold(modified, "true") {
		revision += "-dirty"
	}
	return revision
}
