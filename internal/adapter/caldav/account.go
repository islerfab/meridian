package caldav

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/islerfab/meridian/internal/adapter"
)

// AccountConfig wires one CalDAV account for calendar discovery (sweep).
type AccountConfig struct {
	Endpoint   string
	Username   string
	Password   string
	InstanceID string
	// AccountName is the logical account name (labels for discovered
	// calendars: "<account>?<path>").
	AccountName string
}

// Account implements adapter.AccountSweeper for CalDAV: principal → home
// set → calendar collections.
type Account struct {
	client *caldav.Client
	cfg    AccountConfig
	log    *slog.Logger
}

var _ adapter.AccountSweeper = (*Account)(nil)

// NewAccount builds the discovery client (no I/O).
func NewAccount(cfg AccountConfig, log *slog.Logger) (*Account, error) {
	if cfg.Endpoint == "" || cfg.InstanceID == "" {
		return nil, fmt.Errorf("account %s: endpoint and instanceID are required", cfg.AccountName)
	}
	httpClient := webdav.HTTPClientWithBasicAuth(nil, cfg.Username, cfg.Password)
	client, err := caldav.NewClient(httpClient, cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", cfg.AccountName, err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &Account{client: client, cfg: cfg, log: log}, nil
}

// Discover lists every calendar collection of the account and returns a
// shadow-capable adapter per collection (ProviderID = collection path).
func (a *Account) Discover(ctx context.Context) ([]adapter.DiscoveredCalendar, error) {
	principal, err := a.client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("account %s: find principal: %w: %w", a.cfg.AccountName, err, classify(err))
	}
	homeSet, err := a.client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("account %s: find home set: %w: %w", a.cfg.AccountName, err, classify(err))
	}
	cals, err := a.client.FindCalendars(ctx, homeSet)
	if err != nil {
		return nil, fmt.Errorf("account %s: find calendars: %w: %w", a.cfg.AccountName, err, classify(err))
	}
	var out []adapter.DiscoveredCalendar
	for _, cal := range cals {
		supportsEvents := len(cal.SupportedComponentSet) == 0
		for _, comp := range cal.SupportedComponentSet {
			if comp == "VEVENT" {
				supportsEvents = true
			}
		}
		if !supportsEvents {
			continue
		}
		ad, err := New(Config{
			Endpoint:     a.cfg.Endpoint,
			Username:     a.cfg.Username,
			Password:     a.cfg.Password,
			CalendarPath: cal.Path,
			CalendarID:   a.cfg.AccountName + "?" + cal.Path,
			InstanceID:   a.cfg.InstanceID,
		}, a.log)
		if err != nil {
			return nil, err
		}
		// Trailing slash normalized so config paths compare equal.
		out = append(out, adapter.DiscoveredCalendar{ProviderID: strings.TrimSuffix(cal.Path, "/"), Adapter: ad})
	}
	return out, nil
}
