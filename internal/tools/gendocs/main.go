// Command gendocs regenerates the code-derived data files the docs site
// renders its reference pages from, so those pages can never silently
// drift from the thing they describe — and, just as importantly, can't
// be casually hand-edited: docs/content/reference/cli.md and
// configuration.md are each just front matter plus a single shortcode
// call, and the actual facts live in docs/data/*.json. Nobody opening a
// JSON array mistakes it for prose to fix a typo in.
//
//   - cli.json is walked from the real *cli.Command tree built by
//     internal/cli (the same tree --help renders from).
//   - config.json is parsed from internal/config/config.go's own
//     yaml/doc-tagged struct fields and doc comments (and cel.go's
//     CELEvent for the CEL schema).
//   - metrics.json is parsed from internal/sync/metrics.go's own
//     prometheus.XxxOpts Name/Help declarations.
//
// Rendering (headings, per-item detail boxes, cross-page "see also"
// links) lives entirely in Hugo templates
// (docs/layouts/shortcodes/*-docs.html and supporting partials) — this
// tool only extracts facts, it knows nothing about markdown or HTML.
//
// Usage:
//
//	go run ./internal/tools/gendocs         # regenerate docs/data/*.json in place
//	go run ./internal/tools/gendocs -check  # fail (exit 1) if regenerating would change anything
package main

import (
	"flag"
	"fmt"
	"os"
)

const dataDir = "docs/data"

func main() {
	check := flag.Bool("check", false, "fail if regenerating would change any committed output")
	flag.Parse()

	files, err := buildDataFiles()
	if err != nil {
		fatal(err)
	}

	if *check {
		if err := checkDataFiles(files); err != nil {
			fatal(err)
		}
		return
	}
	if err := writeDataFiles(files); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gendocs:", err)
	os.Exit(1)
}

// buildDataFiles returns the full set of generated data files, keyed by
// path relative to the repo root.
func buildDataFiles() (map[string]string, error) {
	cli, err := genCLIData()
	if err != nil {
		return nil, fmt.Errorf("cli.json: %w", err)
	}
	config, err := genConfigData()
	if err != nil {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	metrics, err := genMetricsData()
	if err != nil {
		return nil, fmt.Errorf("metrics.json: %w", err)
	}
	return map[string]string{
		dataDir + "/cli.json":     cli,
		dataDir + "/config.json":  config,
		dataDir + "/metrics.json": metrics,
	}, nil
}

func writeDataFiles(files map[string]string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

func checkDataFiles(files map[string]string) error {
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("stale: %s: %w — run `go run ./internal/tools/gendocs`", path, err)
		}
		if string(got) != want {
			return fmt.Errorf("stale: %s does not match the generated content — run `go run ./internal/tools/gendocs`", path)
		}
	}
	return nil
}
