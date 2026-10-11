// Package version provides centralized build and runtime version introspection.
package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
)

// Build variables populated via ldflags during build or release.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info holds detailed build and environment metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the populated version Info, inspecting debug.ReadBuildInfo()
// when defaults are present to retrieve VCS revision and commit time if available.
func Get() Info {
	v := Version
	c := Commit
	d := Date

	if (c == "none" || d == "unknown") {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range bi.Settings {
				switch setting.Key {
				case "vcs.revision":
					if c == "none" && setting.Value != "" {
						c = setting.Value
					}
				case "vcs.time":
					if d == "unknown" && setting.Value != "" {
						d = setting.Value
					}
				}
			}
		}
	}

	return Info{
		Version:   v,
		Commit:    c,
		Date:      d,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String formats a human-readable representation of version info:
//
//	election-bot <version>
//	Commit: <commit>
//	Go: <go-version>
//	Platform: <os>/<arch>
func String() string {
	info := Get()
	return fmt.Sprintf("election-bot %s\nCommit: %s\nGo: %s\nPlatform: %s",
		info.Version,
		info.Commit,
		info.GoVersion,
		info.Platform,
	)
}

// JSON returns an indented JSON string representation of version info.
func JSON() (string, error) {
	info := Get()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
