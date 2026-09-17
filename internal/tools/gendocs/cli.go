package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	meridiancli "github.com/islerfab/meridian/internal/cli"
)

// cliFlag and cliCommand mirror the real *cli.Flag / *cli.Command tree as
// plain data, marshaled to docs/data/cli.json. Rendering (headings, the
// details boxes, cross-page "see also" links) is Hugo template code
// (docs/layouts/shortcodes/cli-docs.html and friends) — this file's only
// job is extracting facts from the real command tree, nothing about
// markdown or HTML.
type cliFlag struct {
	Names    []string `json:"names"`
	Default  string   `json:"default,omitempty"`
	Env      []string `json:"env,omitempty"`
	Usage    string   `json:"usage"`
	Required bool     `json:"required"`
}

type cliCommand struct {
	Name        string       `json:"name"`
	Path        string       `json:"path"` // full space-joined path, e.g. "meridian wipe calendar"
	Usage       string       `json:"usage,omitempty"`
	Description string       `json:"description,omitempty"`
	ArgsUsage   string       `json:"argsUsage,omitempty"`
	Flags       []cliFlag    `json:"flags,omitempty"`
	Subcommands []cliCommand `json:"subcommands,omitempty"`
}

// genCLIData walks the real command tree and returns it as JSON.
// cmd.FullName()/.Walk() are unreliable here — urfave/cli only wires each
// Command's parent pointer during Run(), which this tool deliberately
// never calls (no side effects, no flag parsing) — so the tree is walked
// manually, carrying the qualified path down explicitly instead.
func genCLIData() (string, error) {
	root := meridiancli.NewRootCmd()
	commands := make([]cliCommand, 0, len(root.Commands))
	for _, cmd := range root.Commands {
		commands = append(commands, buildCLICommand(cmd, []string{root.Name}))
	}
	out, err := json.MarshalIndent(struct {
		Commands []cliCommand `json:"commands"`
	}{commands}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

func buildCLICommand(cmd *cli.Command, parentPath []string) cliCommand {
	path := make([]string, len(parentPath)+1)
	copy(path, parentPath)
	path[len(parentPath)] = cmd.Name

	out := cliCommand{
		Name:        cmd.Name,
		Path:        strings.Join(path, " "),
		Usage:       cmd.Usage,
		Description: cmd.Description,
		ArgsUsage:   cmd.ArgsUsage,
	}
	for _, f := range cmd.VisibleFlags() {
		dg, ok := f.(cli.DocGenerationFlag)
		if !ok {
			continue
		}
		flag := cliFlag{
			Names:   flagNameList(f),
			Default: defaultText(f, dg),
			Env:     dg.GetEnvVars(),
			Usage:   dg.GetUsage(),
		}
		if req, ok := f.(cli.RequiredFlag); ok {
			flag.Required = req.IsRequired()
		}
		out.Flags = append(out.Flags, flag)
	}
	for _, sub := range cmd.Commands {
		out.Subcommands = append(out.Subcommands, buildCLICommand(sub, path))
	}
	return out
}

// flagNameList renders every name/alias the flag responds to, e.g. --once
// or --client-id; single-character names get one dash.
func flagNameList(f cli.Flag) []string {
	names := make([]string, 0, len(f.Names()))
	for _, n := range f.Names() {
		dash := "--"
		if len(n) == 1 {
			dash = "-"
		}
		names = append(names, dash+n)
	}
	return names
}

// defaultText prefers an explicit DefaultText override; otherwise it
// formats the flag's actual declared Value. GetDefaultText alone isn't
// enough: it only reflects an explicit override, not the Value a flag was
// constructed with. --config's Value: "rules.yaml" doesn't surface through
// GetDefaultText at all.
func defaultText(f cli.Flag, dg cli.DocGenerationFlag) string {
	if !dg.IsDefaultVisible() {
		return ""
	}
	if v := dg.GetDefaultText(); v != "" {
		return v
	}
	switch v := f.Get().(type) {
	case string:
		return v
	case bool, int, int64, float64:
		return fmt.Sprintf("%v", v)
	default:
		return ""
	}
}
