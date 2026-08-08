// Command adapter-smoke live-tests an adapter's five-method contract
// (mer-891 CalDAV, mer-3cc Google) against a real scratch calendar:
// Create → ListShadows → Update → ListEvents (marker visible) → Delete
// (twice — idempotency) → ListShadows empty. Throwaway; credentials from
// .env like hack/spike-expand.
//
//	go run ./hack/adapter-smoke -provider caldav|google
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/adapter/caldav"
	"github.com/islerfab/meridian/internal/adapter/google"
	"github.com/islerfab/meridian/internal/model"
)

func main() {
	provider := flag.String("provider", "caldav", "adapter under test: caldav | google")
	flag.Parse()
	_ = godotenv.Load()
	ctx := context.Background()

	var a adapter.CalendarAdapter
	var err error
	switch *provider {
	case "caldav":
		calPath := os.Getenv("MERIDIAN_SPIKE_CALENDAR")
		if calPath == "" {
			fatal("MERIDIAN_SPIKE_CALENDAR must be set to the spike calendar path")
		}
		a, err = caldav.New(caldav.Config{
			Endpoint:     envOr("MERIDIAN_SPIKE_ENDPOINT", "https://sync.infomaniak.com"),
			Username:     os.Getenv("MERIDIAN_SPIKE_USER"),
			Password:     os.Getenv("MERIDIAN_SPIKE_PASS"),
			CalendarPath: calPath,
			CalendarID:   "spike/test",
			InstanceID:   "smoke-instance",
		}, slog.Default())
	case "google":
		a, err = google.New(ctx, google.Config{
			ClientID:         os.Getenv("MERIDIAN_GOOGLE_CLIENT_ID"),
			ClientSecret:     os.Getenv("MERIDIAN_GOOGLE_CLIENT_SECRET"),
			RefreshToken:     os.Getenv("MERIDIAN_GOOGLE_REFRESH_TOKEN"),
			CalendarID:       "sandbox/google",
			GoogleCalendarID: os.Getenv("MERIDIAN_GOOGLE_CALENDAR_ID"),
			InstanceID:       "smoke-instance",
		}, slog.Default())
	default:
		fatal("unknown provider %q", *provider)
	}
	if err != nil {
		fatal("new adapter: %v", err)
	}
	window := adapter.Window{Start: time.Now().UTC().Add(-24 * time.Hour), End: time.Now().UTC().Add(90 * 24 * time.Hour)}

	content := model.ShadowContent{
		Title:       "smoke shadow",
		Description: "created by hack/adapter-smoke",
		Start:       time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour),
		End:         time.Now().UTC().Add(49 * time.Hour).Truncate(time.Hour),
		Transparent: true,
		Reminders:   []int{15},
	}
	src := model.EventRef{Calendar: "private/main", UID: "smoke-uid", RecurrenceID: ""}
	shadow := model.Shadow{Content: content, Marker: model.NewMarker("smoke-instance", src, "smoke-rule", content)}

	step("Create", a.Create(ctx, shadow))

	shadows := mustShadows(ctx, a, window, 1)
	got := shadows[0]
	if got.Marker.Hash != shadow.Marker.Hash {
		fatal("hash mismatch after create: stored %s, want %s", got.Marker.Hash, shadow.Marker.Hash)
	}
	if model.ContentHash(got.Content) != shadow.Marker.Hash {
		fatal("read-back content hashes to %s, marker says %s — round-trip lossy", model.ContentHash(got.Content), shadow.Marker.Hash)
	}
	fmt.Println("OK   ListShadows: 1 shadow, marker hash matches read-back content hash")

	// Update with changed content at the same object.
	content.Title = "smoke shadow v2"
	updated := model.Shadow{Ref: got.Ref, Content: content, Marker: model.NewMarker("smoke-instance", src, "smoke-rule", content)}
	step("Update", a.Update(ctx, updated))
	shadows = mustShadows(ctx, a, window, 1)
	if shadows[0].Content.Title != "smoke shadow v2" || shadows[0].Marker.Hash != updated.Marker.Hash {
		fatal("update not reflected: %+v", shadows[0])
	}
	fmt.Println("OK   Update: full replace reflected, new hash stored")

	// The shadow must be visible as a marker-carrying event to ListEvents
	// (zombie-guard input when a destination doubles as a source).
	events, err := a.ListEvents(ctx, window)
	step("ListEvents", err)
	foundOwned := false
	for _, ev := range events {
		if ev.Marker != nil && ev.Marker.Rule == "smoke-rule" {
			foundOwned = true
		}
	}
	if !foundOwned {
		fatal("shadow not visible as owned event in ListEvents")
	}
	fmt.Println("OK   ListEvents: shadow surfaces with non-nil Marker")

	step("Delete", a.Delete(ctx, shadows[0].Ref))
	step("Delete again (idempotent)", a.Delete(ctx, shadows[0].Ref))
	mustShadows(ctx, a, window, 0)
	fmt.Println("OK   Delete: idempotent, calendar clean")

	fmt.Println("\nRESULT: ADAPTER SMOKE PASS")
}

func mustShadows(ctx context.Context, a adapter.CalendarAdapter, w adapter.Window, want int) []model.Shadow {
	shadows, err := a.ListShadows(ctx, w)
	step("ListShadows", err)
	if len(shadows) != want {
		fatal("expected %d shadows, got %d: %+v", want, len(shadows), shadows)
	}
	return shadows
}

func step(name string, err error) {
	if err != nil {
		fatal("%s: %v", name, err)
	}
	fmt.Printf("OK   %s\n", name)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "adapter-smoke: FAIL: "+format+"\n", args...)
	os.Exit(1)
}
