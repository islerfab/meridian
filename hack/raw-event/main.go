// Command raw-event dumps the raw Google API JSON (incl. created/updated
// timestamps the adapter abstraction hides) for one meridian shadow,
// looked up by its marker src ref. Stage 2 forensics helper.
//
// Usage: go run ./hack/raw-event -src 'spike/main|meridian-spike-dst|20261101T080000Z'
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

const calendarID = "68e3b714c59386ab4b5af44b3a87a0c18173969158bcbb2a9722fbe6b47384fa@group.calendar.google.com"

func main() {
	src := flag.String("src", "", "marker src ref (meridian.src value)")
	flag.Parse()
	if *src == "" {
		fmt.Fprintln(os.Stderr, "usage: raw-event -src '<marker src ref>'")
		os.Exit(2)
	}
	_ = godotenv.Load()
	ctx := context.Background()
	conf := &oauth2.Config{
		ClientID:     os.Getenv("MERIDIAN_GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("MERIDIAN_GOOGLE_CLIENT_SECRET"),
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
	}
	ts := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: os.Getenv("MERIDIAN_GOOGLE_REFRESH_TOKEN")})
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		fatal(err)
	}
	events, err := svc.Events.List(calendarID).
		PrivateExtendedProperty("meridian.src=" + *src).
		ShowDeleted(true).Do()
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "matched: %d event(s)\n", len(events.Items))
	for _, ev := range events.Items {
		out, _ := json.MarshalIndent(map[string]any{
			"id": ev.Id, "status": ev.Status, "created": ev.Created, "updated": ev.Updated,
			"summary": ev.Summary, "start": ev.Start, "end": ev.End,
			"sequence": ev.Sequence, "extendedProperties": ev.ExtendedProperties,
		}, "", "  ")
		fmt.Println(string(out))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "raw-event:", err)
	os.Exit(1)
}
