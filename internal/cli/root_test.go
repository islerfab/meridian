package cli

import (
	"bytes"
	"context"
	"runtime/debug"
	"strings"
	"testing"
)

func TestRootCommandTree(t *testing.T) {
	root := NewRootCmd()
	got := map[string]bool{}
	for _, c := range root.Commands {
		got[c.Name] = true
	}
	for _, name := range []string{"run", "oauth", "version"} {
		if !got[name] {
			t.Errorf("subcommand %q not found", name)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.Writer = &out
	if err := root.Run(context.Background(), []string{"meridian", "version"}); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if !strings.HasPrefix(out.String(), "meridian ") {
		t.Errorf("unexpected version output: %q", out.String())
	}
}

func TestVersionStringNoBuildInfo(t *testing.T) {
	if got := versionString(nil, false); got != "meridian (no build info)" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestVersionStringInjected(t *testing.T) {
	version, commit, date = "0.1.0", "abcdef1234567890", "2026-08-04T00:00:00Z"
	t.Cleanup(func() { version, commit, date = "", "", "" })

	got := versionString(&debug.BuildInfo{GoVersion: "go1.26.5"}, true)
	want := "meridian 0.1.0 (commit abcdef123456, built 2026-08-04T00:00:00Z) go1.26.5"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVersionStringDirtyCommit(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "vcs.time", Value: "2026-08-04T00:00:00Z"},
		},
	}
	info.Main.Version = "v0.1.0"
	got := versionString(info, true)
	want := "meridian v0.1.0 (commit abcdef123456-dirty, built 2026-08-04T00:00:00Z) go1.26.5"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
