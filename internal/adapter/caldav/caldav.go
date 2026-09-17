// Package caldav implements the CalendarAdapter contract against CalDAV
// servers (Infomaniak/sabre-dav is the reference deployment) via go-webdav
// v0.7.0. Recurrence expansion is server-side through the calendar-query
// REPORT with <expand>, gate-verified against sabre/dav 4.3.1. Shadow
// listing fetches the window and filters X-MERIDIAN-* markers client-side.
package caldav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

// Config wires one CalDAV calendar collection into the engine.
type Config struct {
	// Endpoint is the server base URL, e.g. https://sync.infomaniak.com.
	Endpoint string
	// Username is the CalDAV login. Infomaniak: the internal account ID
	// (USERxxxxx), NOT the email — email auth appears to succeed but
	// principal lookups 404 (see design.md).
	Username string
	Password string
	// CalendarPath is the collection path, e.g. /calendars/USERxxxxx/<uuid>/.
	CalendarPath string
	// CalendarID is the logical calendar reference used in EventRef.Calendar
	// and ShadowRef.Calendar (the config-file name, e.g. "private/main").
	CalendarID string
	// InstanceID scopes ListShadows to this meridian instance's shadows
	// (required; markers of other instances are invisible).
	InstanceID string
}

// Adapter implements adapter.CalendarAdapter for one CalDAV collection.
type Adapter struct {
	client *caldav.Client
	cfg    Config
	log    *slog.Logger
}

var _ adapter.CalendarAdapter = (*Adapter)(nil)

// New validates the config and builds the adapter. It performs no I/O;
// readiness probing happens at engine startup.
func New(cfg Config, log *slog.Logger) (*Adapter, error) {
	if cfg.Endpoint == "" || cfg.CalendarPath == "" {
		return nil, fmt.Errorf("calendar %s: endpoint and calendarPath are required", cfg.CalendarID)
	}
	if cfg.InstanceID == "" {
		return nil, fmt.Errorf("calendar %s: instanceID is required", cfg.CalendarID)
	}
	httpClient := webdav.HTTPClientWithBasicAuth(nil, cfg.Username, cfg.Password)
	client, err := caldav.NewClient(httpClient, cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("calendar %s: %w", cfg.CalendarID, err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{client: client, cfg: cfg, log: log.With("calendar", cfg.CalendarID)}, nil
}

// ListEvents fetches server-expanded instances overlapping the window.
func (a *Adapter) ListEvents(ctx context.Context, window adapter.Window) ([]model.Event, error) {
	objs, err := a.query(ctx, window, true)
	if err != nil {
		return nil, a.normalizeError("list events", err)
	}
	var events []model.Event
	for _, obj := range objs {
		for _, comp := range obj.Data.Children {
			if comp.Name != ical.CompEvent {
				continue
			}
			ev, err := eventFromComponent(comp, a.cfg.CalendarID)
			if err != nil {
				a.log.Warn("skipping unparseable source event", "path", obj.Path, "err", err)
				continue
			}
			events = append(events, ev)
		}
	}
	return events, nil
}

// ListShadows fetches the window (no expand — shadows are discrete single
// events) and keeps only meridian-owned objects.
func (a *Adapter) ListShadows(ctx context.Context, window adapter.Window) ([]model.Shadow, error) {
	objs, err := a.query(ctx, window, false)
	if err != nil {
		return nil, a.normalizeError("list shadows", err)
	}
	var shadows []model.Shadow
	for _, obj := range objs {
		for _, comp := range obj.Data.Children {
			if comp.Name != ical.CompEvent {
				continue
			}
			marker, found, err := parseComponentMarker(comp)
			if !found {
				continue // foreign event: never touched
			}
			if err != nil {
				// Owned-looking but broken: skipping means it is never
				// updated or GC'd — loud is mandatory.
				a.log.Error("malformed meridian marker on destination event", "path", obj.Path, "err", err)
				continue
			}
			if marker.Instance != a.cfg.InstanceID {
				continue // another meridian instance's shadow: invisible
			}
			content, err := contentFromComponent(comp)
			if err != nil {
				a.log.Error("unparseable meridian shadow", "path", obj.Path, "err", err)
				continue
			}
			shadows = append(shadows, model.Shadow{
				Ref:     model.ShadowRef{Calendar: a.cfg.CalendarID, ID: obj.Path},
				Content: content,
				Marker:  marker,
			})
		}
	}
	return shadows, nil
}

// Create writes a new shadow object under a fresh UID.
func (a *Adapter) Create(ctx context.Context, shadow model.Shadow) error {
	uid := "meridian-" + randomHex(16)
	path := strings.TrimSuffix(a.cfg.CalendarPath, "/") + "/" + uid + ".ics"
	cal := buildShadowCalendar(uid, shadow)
	if _, err := a.client.PutCalendarObject(ctx, path, cal); err != nil {
		return a.normalizeError("create", err)
	}
	return nil
}

// Update fully replaces the object at shadow.Ref.ID (content + marker in
// one PUT — last-writer-wins by design).
func (a *Adapter) Update(ctx context.Context, shadow model.Shadow) error {
	uid := uidFromPath(shadow.Ref.ID)
	if uid == "" {
		return fmt.Errorf("update: cannot derive UID from path %q: %w", shadow.Ref.ID, adapter.ErrNotFound)
	}
	cal := buildShadowCalendar(uid, shadow)
	if _, err := a.client.PutCalendarObject(ctx, shadow.Ref.ID, cal); err != nil {
		return a.normalizeError("update", err)
	}
	return nil
}

// Delete removes the object; already-gone is success.
func (a *Adapter) Delete(ctx context.Context, ref model.ShadowRef) error {
	if err := a.client.RemoveAll(ctx, ref.ID); err != nil {
		norm := a.normalizeError("delete", err)
		if errors.Is(norm, adapter.ErrNotFound) {
			return nil
		}
		return norm
	}
	return nil
}

func (a *Adapter) query(ctx context.Context, window adapter.Window, expand bool) ([]caldav.CalendarObject, error) {
	comp := caldav.CalendarCompRequest{
		Name: ical.CompCalendar,
		Comps: []caldav.CalendarCompRequest{{
			Name:     ical.CompEvent,
			AllProps: true,
		}},
	}
	if expand {
		comp.Expand = &caldav.CalendarExpandRequest{Start: window.Start, End: window.End}
	}
	eventFilter := caldav.CompFilter{Name: ical.CompEvent}
	if !window.IsZero() {
		eventFilter.Start = window.Start
		eventFilter.End = window.End
	}
	query := &caldav.CalendarQuery{
		CompRequest: comp,
		CompFilter: caldav.CompFilter{
			Name:  ical.CompCalendar,
			Comps: []caldav.CompFilter{eventFilter},
		},
	}
	return a.client.QueryCalendar(ctx, a.cfg.CalendarPath, query)
}

// --- error normalization ---------------------------------------------------

// go-webdav's typed HTTPError lives in an internal package, so the status
// code is only reachable through the error string ("404 Not Found: ...").
// Contained here; if go-webdav ever exports the type, switch to errors.As.
var httpStatusRe = regexp.MustCompile(`^([1-5][0-9]{2}) `)

func (a *Adapter) normalizeError(op string, err error) error {
	class := classify(err)
	return fmt.Errorf("caldav %s %s: %w: %w", a.cfg.CalendarID, op, err, class)
}

func classify(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return adapter.ErrTransient // network/transport layer
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		m := httpStatusRe.FindStringSubmatch(e.Error())
		if m == nil {
			continue
		}
		switch m[1] {
		case "404", "410":
			return adapter.ErrNotFound
		case "401", "403":
			return adapter.ErrAuthFailed
		case "429":
			return adapter.ErrRateLimited
		}
		return adapter.ErrTransient // other 4xx/5xx: nothing actionable beyond retry-next-cycle
	}
	return adapter.ErrTransient
}

// --- helpers ---------------------------------------------------------------

func uidFromPath(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	return strings.TrimSuffix(base, ".ics")
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

// now is stubbed in tests (DTSTAMP determinism).
var now = time.Now
