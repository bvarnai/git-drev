package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Default build variables injected at compile time via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info holds structured version metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the resolved version metadata, inspecting runtime/debug.BuildInfo
// if compile-time ldflags were not specified.
func Get() Info {
	v := Version
	c := Commit
	d := Date
	injectedCommit := Commit != "none" && Commit != ""

	if bi, ok := debug.ReadBuildInfo(); ok {
		if (v == "dev" || v == "") && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "none" || c == "" {
					if len(s.Value) > 7 {
						c = s.Value[:7]
					} else {
						c = s.Value
					}
				}
			case "vcs.time":
				if d == "unknown" || d == "" {
					d = s.Value
				}
			case "vcs.modified":
				if !injectedCommit && s.Value == "true" && c != "none" && c != "" {
					c += "-dirty"
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

// String formats the version text per docs/spec/global.md:
// "git-drev version <semver> (<commit> <os>/<arch>)"
func String() string {
	info := Get()
	return fmt.Sprintf("git-drev version %s (%s %s)", info.Version, info.Commit, info.Platform)
}
