// Command shadow-list prints every meridian-owned shadow on a configured
// calendar (marker src ref, hash, times) — Stage 2 E2E verification helper
// for diffing destination state against source expansion.
//
// Usage: go run ./hack/shadow-list -config hack/rules-sandbox.yaml -calendar sandbox/main
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/joho/godotenv"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/config"
	"github.com/islerfab/meridian/internal/model"
)

func main() {
	cfgPath := flag.String("config", "hack/rules-sandbox.yaml", "rules.yaml path")
	calendar := flag.String("calendar", "sandbox/main", "logical calendar key (account/calendar)")
	events := flag.Bool("events", false, "list ALL events via ListEvents (foreign + owned) instead of shadows; needs a real window")
	flag.Parse()
	_ = godotenv.Load()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}
	adapters, err := config.BuildAdapters(context.Background(), cfg, log)
	if err != nil {
		fatal(err)
	}
	ad, ok := adapters[*calendar]
	if !ok {
		fatal(fmt.Errorf("%q is not a configured calendar", *calendar))
	}
	if *events {
		w := adapter.Window{Start: time.Now().Add(-24 * time.Hour).UTC(), End: time.Now().Add(90 * 24 * time.Hour).UTC()}
		evs, err := ad.ListEvents(context.Background(), w)
		if err != nil {
			fatal(err)
		}
		sort.Slice(evs, func(i, j int) bool { return evs[i].Start.Before(evs[j].Start) })
		for _, ev := range evs {
			owned := "foreign"
			if ev.Marker != nil {
				owned = "OWNED rule=" + ev.Marker.Rule
			}
			fmt.Printf("%-60s start=%s  %s  title=%q\n",
				ev.Ref.String(), ev.Start.UTC().Format("2006-01-02T15:04:05Z"), owned, ev.Title)
		}
		fmt.Fprintf(os.Stderr, "total: %d event(s)\n", len(evs))
		return
	}
	shadows, err := ad.ListShadows(context.Background(), adapter.Window{}) // unbounded
	if err != nil {
		fatal(err)
	}
	sort.Slice(shadows, func(i, j int) bool {
		return shadows[i].Content.Start.Before(shadows[j].Content.Start)
	})
	verbose := os.Getenv("SHADOW_LIST_VERBOSE") != ""
	for _, s := range shadows {
		fmt.Printf("%s  start=%s  allday=%v  rule=%s  hash=%.12s  title=%q\n",
			s.Marker.Src.String(), s.Content.Start.UTC().Format("2006-01-02T15:04:05Z"),
			s.Content.AllDay, s.Marker.Rule, s.Marker.Hash, s.Content.Title)
		if verbose {
			fmt.Printf("    observedHash=%.12s  end=%s  transparent=%v  reminders=%v\n    description=%q\n    location=%q\n",
				model.ContentHash(s.Content), s.Content.End.UTC().Format("2006-01-02T15:04:05Z"),
				s.Content.Transparent, s.Content.Reminders, s.Content.Description, s.Content.Location)
		}
	}
	fmt.Fprintf(os.Stderr, "total: %d shadow(s)\n", len(shadows))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "shadow-list:", err)
	os.Exit(1)
}
