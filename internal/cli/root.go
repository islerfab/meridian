// Package cli wires up the meridian command tree (urfave/cli v3).
package cli

import (
	"context"

	"github.com/urfave/cli/v3"
)

// Execute runs the root command against the given arguments (os.Args form,
// program name included).
func Execute(ctx context.Context, args []string) error {
	return NewRootCmd().Run(ctx, args)
}

// NewRootCmd builds the full command tree. Constructed fresh per call so
// tests can run commands in isolation.
func NewRootCmd() *cli.Command {
	return &cli.Command{
		Name:  "meridian",
		Usage: "K8s-native declarative calendar sync",
		Description: "Meridian mirrors events between calendars as transformed shadow copies,\n" +
			"driven by declarative rules and stateless snapshot reconciliation.",
		Commands: []*cli.Command{
			newRunCmd(),
			newValidateCmd(),
			newWipeCmd(),
			newOAuthCmd(),
			newVersionCmd(),
		},
	}
}
