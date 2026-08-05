package cli

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

// newRunCmd starts the reconciliation loop. Implemented by the sync engine
// issues (mer-e6u and successors).
func newRunCmd() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "Start the reconciliation loop",
		Action: func(_ context.Context, _ *cli.Command) error {
			return errors.New("run: not implemented yet")
		},
	}
}
