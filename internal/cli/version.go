package cli

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/urfave/cli/v3"
)

func newVersionCmd() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print build version information",
		Description: "Tag, commit, and build date via -ldflags in release builds; falls back to Go's\n" +
			"toolchain-embedded build info (debug.ReadBuildInfo) for local builds.",
		Action: func(_ context.Context, cmd *cli.Command) error {
			_, err := fmt.Fprintln(cmd.Root().Writer, versionString(debug.ReadBuildInfo()))
			return err
		},
	}
}

// Release builds get these injected by GoReleaser (see .goreleaser.yaml
// ldflags). Local `go build` leaves them empty and versionString falls back
// to the toolchain-embedded build info.
var (
	version string
	commit  string
	date    string
)

// versionString renders version info: GoReleaser-injected vars when present,
// otherwise the toolchain-embedded build info (module version stamped from
// the VCS tag/commit since Go 1.24, plus vcs.* build settings).
func versionString(info *debug.BuildInfo, ok bool) string {
	if version != "" {
		out := "meridian " + version
		if commit != "" {
			out += " (commit " + shortRev(commit)
			if date != "" {
				out += ", built " + date
			}
			out += ")"
		}
		if ok && info != nil && info.GoVersion != "" {
			out += " " + info.GoVersion
		}
		return out
	}
	if !ok || info == nil {
		return "meridian (no build info)"
	}

	var revision, modified, buildTime string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		case "vcs.time":
			buildTime = s.Value
		}
	}

	out := "meridian " + info.Main.Version
	if revision != "" {
		rev := shortRev(revision)
		if modified == "true" {
			rev += "-dirty"
		}
		out += fmt.Sprintf(" (commit %s", rev)
		if buildTime != "" {
			out += ", built " + buildTime
		}
		out += ")"
	}
	if info.GoVersion != "" {
		out += " " + info.GoVersion
	}
	return out
}

func shortRev(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
