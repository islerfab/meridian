package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"

	"github.com/islerfab/meridian/internal/adapter/caldav"
	"github.com/islerfab/meridian/internal/config"
)

// newIdentitiesCmd is the setup-time counterpart to `oauth`: it turns a
// question the operator would otherwise have to guess at — which address
// does this CalDAV server know me by — into an answer from the server.
//
// Like `oauth`, it has to work before a config exists. Naming an account
// reads the connection out of rules.yaml; --endpoint takes it from flags or
// the environment, which is the only form available while still writing the
// config this command helps fill in.
func newIdentitiesCmd() *cli.Command {
	return &cli.Command{
		Name:      "identities",
		Usage:     "Ask a CalDAV server which addresses it knows this account by",
		ArgsUsage: "[account]",
		Description: "Reading your own RSVP off an invitation means picking your entry out of the attendee\n" +
			"list. Google marks it; iCalendar has no equivalent, so a CalDAV account has to name its\n" +
			"own addresses in `identities` before filter.skipDeclined or transform.transparentForRSVP\n" +
			"can work.\n\n" +
			"This asks the server directly, via the calendar-user-address-set property, and prints the\n" +
			"answer ready to paste. It also lists the account's calendars and their paths, which a\n" +
			"config needs anyway and which providers tend to bury in a web UI.\n\n" +
			"Run it against a configured account by name, or before any config exists by passing\n" +
			"--endpoint with credentials in the environment or a local .env. Nothing is written: the\n" +
			"config is usually a ConfigMap in git.",
		Flags: []cli.Flag{
			configFlag(),
			&cli.StringFlag{
				Name:    "endpoint",
				Usage:   "CalDAV base URL, to run without a config file",
				Sources: cli.EnvVars("MERIDIAN_CALDAV_ENDPOINT"),
			},
			&cli.StringFlag{
				Name:    "username",
				Usage:   "CalDAV username (prefer MERIDIAN_CALDAV_USERNAME in a .env)",
				Sources: cli.EnvVars("MERIDIAN_CALDAV_USERNAME"),
			},
			&cli.StringFlag{
				Name:    "password",
				Usage:   "CalDAV password (prefer MERIDIAN_CALDAV_PASSWORD in a .env)",
				Sources: cli.EnvVars("MERIDIAN_CALDAV_PASSWORD"),
			},
		},
		Action: runIdentities,
	}
}

func runIdentities(ctx context.Context, cmd *cli.Command) error {
	// Dev convenience, same as oauth: a local .env supplies credentials.
	// Real env vars win and this is a no-op when the file is absent.
	_ = godotenv.Load()

	if cmd.Args().Len() > 1 {
		return fmt.Errorf("usage: meridian identities <account>, or meridian identities --endpoint <url>")
	}
	account := cmd.Args().First()
	endpoint := stringFromFlagOrEnv(cmd, "endpoint", "MERIDIAN_CALDAV_ENDPOINT")
	if account != "" && endpoint != "" {
		return fmt.Errorf("identities: give an account name or --endpoint, not both")
	}

	conn, err := identitiesConnection(cmd, account, endpoint)
	if err != nil {
		return err
	}
	found, err := caldav.Discover(ctx, conn)
	if err != nil {
		return err
	}
	printIdentities(cmd, account, conn.Endpoint, found)
	return nil
}

// identitiesConnection resolves how to reach the server: out of the config
// when an account is named, out of flags and the environment otherwise.
func identitiesConnection(cmd *cli.Command, account, endpoint string) (caldav.Config, error) {
	if account != "" {
		cfg, err := config.Load(cmd.String("config"))
		if err != nil {
			return caldav.Config{}, err
		}
		return config.CalDAVConnection(cfg, account)
	}
	if endpoint == "" {
		return caldav.Config{}, fmt.Errorf("identities: name a configured account, or pass --endpoint to run without a config")
	}
	user := stringFromFlagOrEnv(cmd, "username", "MERIDIAN_CALDAV_USERNAME")
	pass := stringFromFlagOrEnv(cmd, "password", "MERIDIAN_CALDAV_PASSWORD")
	if user == "" || pass == "" {
		return caldav.Config{}, fmt.Errorf("identities: --endpoint needs credentials, via --username/--password, " +
			"MERIDIAN_CALDAV_USERNAME/MERIDIAN_CALDAV_PASSWORD, or a .env carrying those two")
	}
	return caldav.Config{Endpoint: endpoint, Username: user, Password: pass}, nil
}

func printIdentities(cmd *cli.Command, account, endpoint string, found caldav.Discovery) {
	out := cmd.Writer
	if account == "" {
		_, _ = fmt.Fprintf(out, "server %s\n", endpoint)
	} else {
		_, _ = fmt.Fprintf(out, "account %s (%s)\n", account, endpoint)
	}
	_, _ = fmt.Fprintf(out, "  principal: %s\n\n", found.Principal)

	// The label on the left is the server's own, and only here to tell the
	// paths apart. A config's `name:` is the operator's choice, so the
	// column heading says whose name it is rather than letting the table
	// imply the two are the same thing.
	if len(found.Calendars) > 0 {
		_, _ = fmt.Fprintf(out, "  %-40s %s\n", "NAME ON THE SERVER", "PATH")
		for _, c := range found.Calendars {
			cn := c.Name
			if cn == "" {
				cn = "(unnamed)"
			}
			_, _ = fmt.Fprintf(out, "  %-40s %s\n", cn, c.Path)
		}
		_, _ = fmt.Fprintln(out)
	}

	if len(found.Identities) == 0 {
		_, _ = fmt.Fprintf(out, "The server did not report any addresses for this principal. Not every CalDAV\n")
		_, _ = fmt.Fprintf(out, "implementation supports calendar-user-address-set, so set the address you use\n")
		_, _ = fmt.Fprintf(out, "with this account by hand: it is the one that appears as ATTENDEE on\n")
		_, _ = fmt.Fprintf(out, "invitations sent to you.\n\n")
	}

	name := account
	if name == "" {
		name = "<account>"
	}
	identities := "you@example.com"
	if len(found.Identities) > 0 {
		identities = strings.Join(found.Identities, ", ")
	}

	_, _ = fmt.Fprintf(out, "For rules.yaml:\n\n")
	_, _ = fmt.Fprintf(out, "  accounts:\n")
	_, _ = fmt.Fprintf(out, "    - name: %s\n", name)
	_, _ = fmt.Fprintf(out, "      identities: [%s]\n", identities)
	if len(found.Calendars) > 0 {
		_, _ = fmt.Fprintf(out, "      calendars:\n")
		_, _ = fmt.Fprintf(out, "        - name: main\n")
		_, _ = fmt.Fprintf(out, "          path: %s\n", found.Calendars[0].Path)
		_, _ = fmt.Fprintf(out, "\nPaths go in `path`, one entry per calendar you want to use. Each entry's\n")
		_, _ = fmt.Fprintf(out, "`name` is yours to choose, and is how rules refer to it: %s/main\n", name)
	}
}
