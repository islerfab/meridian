package google

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/oauth2"
	calendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/islerfab/meridian/internal/adapter"
)

// AccountConfig wires one Google account for calendar discovery (sweep).
type AccountConfig struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
	InstanceID   string
	AccountName  string
}

// Account implements adapter.AccountSweeper via the CalendarList API.
type Account struct {
	svc *calendar.Service
	cfg AccountConfig
	log *slog.Logger
}

var _ adapter.AccountSweeper = (*Account)(nil)

// NewAccount builds the discovery client (token refresh is lazy).
func NewAccount(ctx context.Context, cfg AccountConfig, log *slog.Logger) (*Account, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RefreshToken == "" || cfg.InstanceID == "" {
		return nil, fmt.Errorf("account %s: client credentials and instanceID are required", cfg.AccountName)
	}
	conf := &oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: oauthEndpoint}
	ts := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: cfg.RefreshToken})
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", cfg.AccountName, err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Account{svc: svc, cfg: cfg, log: log}, nil
}

// Discover lists the account's calendar list and returns a shadow-capable
// adapter per writable calendar (ProviderID = Google calendar ID).
// Read-only calendars (subscriptions, holidays) cannot hold our shadows'
// deletions and are skipped.
func (a *Account) Discover(ctx context.Context) ([]adapter.DiscoveredCalendar, error) {
	var out []adapter.DiscoveredCalendar
	err := a.svc.CalendarList.List().Pages(ctx, func(page *calendar.CalendarList) error {
		for _, entry := range page.Items {
			if entry.AccessRole != "owner" && entry.AccessRole != "writer" {
				continue
			}
			ad, err := New(ctx, Config{
				ClientID:         a.cfg.ClientID,
				ClientSecret:     a.cfg.ClientSecret,
				RefreshToken:     a.cfg.RefreshToken,
				CalendarID:       a.cfg.AccountName + "?" + entry.Id,
				GoogleCalendarID: entry.Id,
				InstanceID:       a.cfg.InstanceID,
			}, a.log)
			if err != nil {
				return err
			}
			out = append(out, adapter.DiscoveredCalendar{ProviderID: entry.Id, Adapter: ad})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("account %s: calendar list: %w: %w", a.cfg.AccountName, err, classify(err))
	}
	return out, nil
}

// ResolveCalendarID returns a calendar's canonical provider ID. Config may
// reference a calendar by alias (e.g. "primary"), but CalendarList.List
// (used by Discover) never returns that alias for the primary calendar —
// only its real ID (the account's email address). Callers that need to
// recognize a configured calendar among discovered ones (the sweep) must
// key on the resolved ID, not the alias, or the primary calendar never
// matches and its own shadows are swept as orphans every cycle.
func (a *Account) ResolveCalendarID(ctx context.Context, id string) (string, error) {
	cal, err := a.svc.Calendars.Get(id).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("account %s: resolve calendar %s: %w: %w", a.cfg.AccountName, id, err, classify(err))
	}
	return cal.Id, nil
}
