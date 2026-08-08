package cli

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"

	"github.com/islerfab/meridian/internal/config"
	"github.com/islerfab/meridian/internal/model"
	"github.com/islerfab/meridian/internal/sync"
)

// newWipeCmd deletes this instance's shadows on demand (mer-uks: explicit
// operator intent replaces automatic rule-removal GC). Only own-instance
// markers are ever touched; listing is unbounded (out-of-window strays too).
func newWipeCmd() *cli.Command {
	yesFlag := &cli.BoolFlag{Name: "yes", Usage: "skip the confirmation prompt"}
	return &cli.Command{
		Name:  "wipe",
		Usage: "Delete this instance's shadow events (explicit cleanup)",
		Commands: []*cli.Command{
			{
				Name:      "calendar",
				Usage:     "Delete ALL of this instance's shadows on one calendar",
				ArgsUsage: "<account/calendar>",
				Flags:     []cli.Flag{configFlag(), yesFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return fmt.Errorf("usage: meridian wipe calendar <account/calendar>")
					}
					return runWipe(ctx, cmd, []string{cmd.Args().First()}, "")
				},
			},
			{
				Name:      "rule",
				Usage:     "Delete one rule's shadows (all calendars, or --calendar)",
				ArgsUsage: "<rule-id>",
				Flags: []cli.Flag{configFlag(), yesFlag,
					&cli.StringFlag{Name: "calendar", Usage: "narrow to one account/calendar"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return fmt.Errorf("usage: meridian wipe rule <rule-id> [--calendar <account/calendar>]")
					}
					var calendars []string
					if cal := cmd.String("calendar"); cal != "" {
						calendars = []string{cal}
					}
					return runWipe(ctx, cmd, calendars, cmd.Args().First())
				},
			},
		},
	}
}

// runWipe plans across the given calendars (empty = every configured
// calendar), shows the plan, confirms, deletes.
func runWipe(ctx context.Context, cmd *cli.Command, calendars []string, rule string) error {
	_ = godotenv.Load()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load(cmd.String("config"))
	if err != nil {
		return err
	}
	adapters, err := config.BuildAdapters(ctx, cfg, log)
	if err != nil {
		return err
	}
	if len(calendars) == 0 {
		for key := range adapters {
			calendars = append(calendars, key)
		}
		sort.Strings(calendars)
	}

	target := sync.WipeTarget{InstanceID: cfg.Instance, Rule: rule}
	plan := map[string][]model.Shadow{}
	total := 0
	for _, cal := range calendars {
		ad, ok := adapters[cal]
		if !ok {
			return fmt.Errorf("wipe: %q is not a configured calendar", cal)
		}
		shadows, err := sync.WipePlan(ctx, ad, target)
		if err != nil {
			return err
		}
		plan[cal] = shadows
		total += len(shadows)
		_, _ = fmt.Fprintf(cmd.Writer, "%-40s %d shadow(s)\n", cal, len(shadows))
	}
	if total == 0 {
		_, _ = fmt.Fprintln(cmd.Writer, "nothing to wipe")
		return nil
	}

	scope := "instance " + cfg.Instance
	if rule != "" {
		scope += ", rule " + rule
	}
	_, _ = fmt.Fprintf(cmd.Writer, "\nAbout to delete %d shadow(s) owned by %s.\n", total, scope)
	if !cmd.Bool("yes") {
		_, _ = fmt.Fprint(cmd.Writer, "Proceed? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			_, _ = fmt.Fprintln(cmd.Writer, "aborted")
			return nil
		}
	}

	for _, cal := range calendars {
		if len(plan[cal]) == 0 {
			continue
		}
		deleted, err := sync.Wipe(ctx, adapters[cal], plan[cal], log)
		if err != nil {
			return fmt.Errorf("%s: %w (deleted %d before failure)", cal, err, deleted)
		}
		_, _ = fmt.Fprintf(cmd.Writer, "%-40s deleted %d\n", cal, deleted)
	}
	return nil
}
