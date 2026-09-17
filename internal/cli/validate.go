package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"github.com/islerfab/meridian/internal/config"
)

// newValidateCmd checks a rules.yaml without touching providers or
// credentials: strict parse + full validation + CEL/template compilation —
// everything `run` does at startup except resolving secret env vars and
// the auth probes. For CI pipelines and pre-deploy checks.
func newValidateCmd() *cli.Command {
	return &cli.Command{
		Name:  "validate",
		Usage: "Validate a rules.yaml (no credentials needed)",
		Description: "Strict parse, every reference check, CEL and template compilation — everything `run`\n" +
			"does at startup except resolving secret env vars and the live auth probes. Safe for CI.",
		Flags: []cli.Flag{configFlag(),
			&cli.BoolFlag{
				Name: "from-configmap",
				Usage: "treat the config file as a k8s ConfigMap manifest and validate its" +
					" data key \"rules.yaml\" (e.g. helm template output, kubectl get cm -o yaml)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			path := cmd.String("config")
			if cmd.Bool("from-configmap") {
				extracted, err := extractConfigMapRules(path)
				if err != nil {
					return err
				}
				defer os.Remove(extracted) //nolint:errcheck
				path = extracted
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if _, err := config.CompileRules(cfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.Writer, "ok: %d account(s), %d rule(s), interval %s\n",
				len(cfg.Accounts), len(cfg.Rules), cfg.IntervalDuration)
			return nil
		},
	}
}

// extractConfigMapRules pulls data["rules.yaml"] out of a ConfigMap
// manifest into a temp file for config.Load (which takes a path).
func extractConfigMapRules(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Kind string            `yaml:"kind"`
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("parsing ConfigMap manifest %s: %w", path, err)
	}
	rules, ok := manifest.Data["rules.yaml"]
	if !ok {
		return "", fmt.Errorf("%s: no data key \"rules.yaml\" (kind %q)", path, manifest.Kind)
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("meridian-validate-%d.yaml", os.Getpid()))
	if err := os.WriteFile(tmp, []byte(rules), 0o600); err != nil {
		return "", err
	}
	return tmp, nil
}
