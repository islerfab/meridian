package cli

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

// newOAuthCmd performs the interactive OAuth bootstrap flow for provider
// accounts. Implemented by mer-d5s.
func newOAuthCmd() *cli.Command {
	return &cli.Command{
		Name:  "oauth",
		Usage: "Interactive OAuth bootstrap for provider accounts",
		Action: func(_ context.Context, _ *cli.Command) error {
			return errors.New("oauth: not implemented yet")
		},
	}
}
