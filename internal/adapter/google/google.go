// Package google implements the CalendarAdapter contract against the Google
// Calendar API v3. Recurrence expansion is server-side via
// events.list(singleEvents=true) (DESIGN.md Decision 3); shadow listing uses
// the server-side privateExtendedProperty query on the marker version key
// (Decision 2 "Google bonus").
package google

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oauth2"
	calendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

// Google OAuth 2.0 endpoints — kept in sync with internal/cli (deliberately
// duplicated two URLs rather than importing the metadata-heavy
// x/oauth2/google package).
var oauthEndpoint = oauth2.Endpoint{
	AuthURL:  "https://accounts.google.com/o/oauth2/auth",
	TokenURL: "https://oauth2.googleapis.com/token",
}

// Config wires one Google calendar into the engine.
type Config struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	// CalendarID is the logical calendar reference used in EventRef.Calendar
	// and ShadowRef.Calendar (the config-file name, e.g. "work/primary").
	CalendarID string
	// GoogleCalendarID is the provider calendar ID
	// (e.g. ...@group.calendar.google.com or "primary").
	GoogleCalendarID string
	// InstanceID scopes ListShadows to this meridian instance's shadows
	// (required; server-side query, other instances' markers never fetched).
	InstanceID string
}

// Adapter implements adapter.CalendarAdapter for one Google calendar.
type Adapter struct {
	svc *calendar.Service
	cfg Config
	log *slog.Logger
}

var _ adapter.CalendarAdapter = (*Adapter)(nil)

// New builds the adapter. The token source refreshes lazily; auth is only
// exercised on first use (readiness probing happens at engine startup).
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Adapter, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RefreshToken == "" {
		return nil, fmt.Errorf("calendar %s: client ID, secret, and refresh token are required", cfg.CalendarID)
	}
	if cfg.GoogleCalendarID == "" {
		return nil, fmt.Errorf("calendar %s: googleCalendarID is required", cfg.CalendarID)
	}
	if cfg.InstanceID == "" {
		return nil, fmt.Errorf("calendar %s: instanceID is required", cfg.CalendarID)
	}
	conf := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     oauthEndpoint,
	}
	ts := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: cfg.RefreshToken})
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("calendar %s: %w", cfg.CalendarID, err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{svc: svc, cfg: cfg, log: log.With("calendar", cfg.CalendarID)}, nil
}

// ListEvents fetches server-expanded instances overlapping the window.
// Cancelled events (tombstones) are skipped, never a cycle failure. So are
// eventType=="birthday" items: Google auto-injects contacts' birthdays
// directly into the primary calendar's events feed, and they are never
// something a sync rule should mirror by default.
//
// To sync birthdays explicitly instead, configure a calendar with
// id: addressbook#contacts@group.v.calendar.google.com — the dedicated
// Birthdays calendar. It's a real, directly addressable calendar resource
// (confirmed via Calendars.Get and Events.List) but never appears in
// CalendarList (not even with showHidden), so the account-wide sweep's
// Discover() can never auto-target it — a rule must name it explicitly.
// Events fetched from it carry eventType=="default", not "birthday", so
// this skip never excludes them there.
func (a *Adapter) ListEvents(ctx context.Context, window adapter.Window) ([]model.Event, error) {
	var events []model.Event
	call := a.svc.Events.List(a.cfg.GoogleCalendarID).
		SingleEvents(true).
		TimeMin(window.Start.UTC().Format(time.RFC3339)).
		TimeMax(window.End.UTC().Format(time.RFC3339)).
		MaxResults(2500)
	err := call.Pages(ctx, func(page *calendar.Events) error {
		for _, item := range page.Items {
			if item.Status == "cancelled" || item.EventType == "birthday" {
				continue
			}
			ev, err := eventFromGoogle(item, a.cfg.CalendarID)
			if err != nil {
				a.log.Warn("skipping unparseable source event", "id", item.Id, "err", err)
				continue
			}
			events = append(events, ev)
		}
		return nil
	})
	if err != nil {
		return nil, a.normalizeError("list events", err)
	}
	return events, nil
}

// ListShadows uses the server-side marker query: only events carrying
// meridian.v=1 in extendedProperties.private come back at all.
func (a *Adapter) ListShadows(ctx context.Context, window adapter.Window) ([]model.Shadow, error) {
	var shadows []model.Shadow
	call := a.svc.Events.List(a.cfg.GoogleCalendarID).
		PrivateExtendedProperty(model.GoogleKeyInstance + "=" + a.cfg.InstanceID).
		SingleEvents(true).
		MaxResults(2500)
	if !window.IsZero() {
		call = call.TimeMin(window.Start.UTC().Format(time.RFC3339)).
			TimeMax(window.End.UTC().Format(time.RFC3339))
	}
	err := call.Pages(ctx, func(page *calendar.Events) error {
		for _, item := range page.Items {
			if item.Status == "cancelled" {
				continue
			}
			shadow, err := shadowFromGoogle(item, a.cfg.CalendarID)
			if err != nil {
				// Owned (it matched the query) but broken: skipping means it
				// is never updated or GC'd — loud is mandatory.
				a.log.Error("malformed meridian shadow", "id", item.Id, "err", err)
				continue
			}
			shadows = append(shadows, shadow)
		}
		return nil
	})
	if err != nil {
		return nil, a.normalizeError("list shadows", err)
	}
	return shadows, nil
}

// Create inserts a new shadow event (content + marker in one write).
func (a *Adapter) Create(ctx context.Context, shadow model.Shadow) error {
	_, err := a.svc.Events.Insert(a.cfg.GoogleCalendarID, googleFromShadow(shadow)).Context(ctx).Do()
	if err != nil {
		return a.normalizeError("create", err)
	}
	return nil
}

// Update fully replaces content + marker in one write (last-writer-wins).
func (a *Adapter) Update(ctx context.Context, shadow model.Shadow) error {
	_, err := a.svc.Events.Update(a.cfg.GoogleCalendarID, shadow.Ref.ID, googleFromShadow(shadow)).Context(ctx).Do()
	if err != nil {
		return a.normalizeError("update", err)
	}
	return nil
}

// Delete removes a shadow; already-gone (404/410) is success.
func (a *Adapter) Delete(ctx context.Context, ref model.ShadowRef) error {
	err := a.svc.Events.Delete(a.cfg.GoogleCalendarID, ref.ID).Context(ctx).Do()
	if err != nil {
		norm := a.normalizeError("delete", err)
		if errors.Is(norm, adapter.ErrNotFound) {
			return nil
		}
		return norm
	}
	return nil
}

// --- error normalization ---------------------------------------------------

func (a *Adapter) normalizeError(op string, err error) error {
	return fmt.Errorf("google %s %s: %w: %w", a.cfg.CalendarID, op, err, classify(err))
}

// rateLimitReasons are 403 reasons that mean throttling, not permissions
// (Google reports most quota errors as 403, not 429).
var rateLimitReasons = map[string]bool{
	"rateLimitExceeded":     true,
	"userRateLimitExceeded": true,
	"quotaExceeded":         true,
	"dailyLimitExceeded":    true,
}

func classify(err error) error {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		// invalid_grant = refresh token revoked/expired: needs a human.
		return adapter.ErrAuthFailed
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return adapter.ErrTransient // network/transport layer
	}
	switch gerr.Code {
	case 404, 410:
		return adapter.ErrNotFound
	case 401:
		return adapter.ErrAuthFailed
	case 403:
		for _, item := range gerr.Errors {
			if rateLimitReasons[item.Reason] {
				return adapter.ErrRateLimited
			}
		}
		return adapter.ErrAuthFailed
	case 429:
		return adapter.ErrRateLimited
	default:
		return adapter.ErrTransient
	}
}
