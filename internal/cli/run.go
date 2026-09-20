package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/urfave/cli/v3"

	"github.com/islerfab/meridian/internal/config"
	"github.com/islerfab/meridian/internal/obs"
	"github.com/islerfab/meridian/internal/sync"
)

func configFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    "config",
		Usage:   "path to rules.yaml",
		Value:   "rules.yaml",
		Sources: cli.EnvVars("MERIDIAN_CONFIG"),
	}
}

// newRunCmd starts the reconciliation loop: load + validate config (all
// CEL/template/wiring failures are startup errors), build adapters, then
// level-triggered cycles until SIGINT/SIGTERM.
func newRunCmd() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "Start the reconciliation loop",
		Description: "Loads and validates config, builds adapters, runs a read-only startup auth probe against\n" +
			"every configured calendar, then cycles on the config's `interval` until SIGINT/SIGTERM.\n\n" +
			"No reconciliation cycle runs (outside --once) until every calendar's auth probe passes —\n" +
			"a pod that never becomes ready has provably written and deleted nothing.",
		Flags: []cli.Flag{configFlag(),
			&cli.BoolFlag{Name: "once", Usage: "run a single cycle and exit"},
			&cli.StringFlag{
				Name:    "listen",
				Usage:   "observability listen address (/metrics, /healthz, /readyz)",
				Value:   "[::]:8080",
				Sources: cli.EnvVars("MERIDIAN_LISTEN"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Dev convenience; a no-op when .env does not exist (in-cluster
			// the env comes from the Secret).
			_ = godotenv.Load()
			log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
			slog.SetDefault(log)

			engine, interval, err := buildEngine(ctx, cmd.String("config"), log)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			if cmd.Bool("once") {
				engine.RunCycle(ctx)
				return nil
			}

			srv := obs.New(cmd.String("listen"), prometheus.DefaultGatherer, log)
			if err := srv.Start(); err != nil {
				return fmt.Errorf("observability listener: %w", err)
			}
			defer func() {
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutCtx)
			}()

			log.Info("meridian starting", "interval", interval.String(), "listen", srv.Addr())
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			// Auth-probe gate: no cycle runs until every calendar's
			// credentials verify, so a pod that never became ready has
			// provably written and deleted nothing.
			for {
				if err := engine.ProbeAuth(ctx); err == nil {
					break
				}
				log.Warn("not ready: auth probes failing, retrying next interval")
				select {
				case <-ctx.Done():
					log.Info("meridian stopping")
					return nil
				case <-ticker.C:
				}
			}
			srv.SetReady()
			log.Info("auth probes passed, ready")

			for {
				engine.RunCycle(ctx)
				select {
				case <-ctx.Done():
					log.Info("meridian stopping")
					return nil
				case <-ticker.C:
				}
			}
		},
	}
}

// buildEngine assembles the full engine from a config file (shared by run;
// wipe builds adapters only).
func buildEngine(ctx context.Context, path string, log *slog.Logger) (*sync.Engine, time.Duration, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, 0, err
	}
	rules, err := config.CompileRules(cfg)
	if err != nil {
		return nil, 0, err
	}
	adapters, err := config.BuildAdapters(ctx, cfg, log)
	if err != nil {
		return nil, 0, err
	}
	notifier, err := config.BuildNotifier(cfg)
	if err != nil {
		return nil, 0, err
	}
	sweepers, calendarKeys, err := config.BuildSweepers(ctx, cfg, log)
	if err != nil {
		return nil, 0, err
	}
	engine, err := sync.New(sync.Config{
		InstanceID:         cfg.Instance,
		Rules:              rules,
		Adapters:           adapters,
		Sweepers:           sweepers,
		CalendarKeys:       calendarKeys,
		MassDeleteFraction: *cfg.Guards.MassDeleteFraction,
		Notifier:           notifier,
		Logger:             log,
		Metrics:            sync.NewMetrics(prometheus.DefaultRegisterer),
	})
	if err != nil {
		return nil, 0, err
	}
	return engine, cfg.IntervalDuration, nil
}
