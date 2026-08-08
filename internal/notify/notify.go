// Package notify is the operator notification channel (DESIGN.md Decision 1
// guard 2 as superseded 2026-08-08): the engine reports suspicious-but-
// executed conditions (mass deletes) instead of freezing on them. Discord
// webhook is the first implementation; Nop is the default.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier delivers one operator-facing message. Implementations must be
// safe for concurrent use; delivery failures are the caller's to log, never
// to act on (notifications are best-effort by design).
type Notifier interface {
	Notify(ctx context.Context, message string) error
}

// Nop discards notifications (the default when no channel is configured).
type Nop struct{}

func (Nop) Notify(context.Context, string) error { return nil }

// Discord posts messages to a Discord webhook URL.
type Discord struct {
	WebhookURL string
	// Client defaults to a 10s-timeout client.
	Client *http.Client
}

func (d *Discord) Notify(ctx context.Context, message string) error {
	body, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("discord webhook: status %d", resp.StatusCode)
	}
	return nil
}
