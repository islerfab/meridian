// Package notify is the operator notification channel: the engine reports
// suspicious-but-executed conditions (mass deletes) instead of freezing on
// them. Nop is the default, and the right choice under Kubernetes, where
// meridian_guard_triggers_total is the signal an alerting stack already
// watches. Webhook exists for deployments without one.
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

// Webhook POSTs each message as JSON to one URL.
//
// The body is {"content": "<message>"}, which is Discord's own incoming-
// webhook shape, so a Discord URL works here with nothing in front of it.
// Anything else wants a small receiver to translate — Slack, for one, reads
// "text" rather than "content". That body is a published contract: fields
// may be added, but "content" keeps carrying the whole message.
type Webhook struct {
	URL string
	// Client defaults to a 10s-timeout client.
	Client *http.Client
}

func (w *Webhook) Notify(ctx context.Context, message string) error {
	body, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("notify webhook: status %d", resp.StatusCode)
	}
	return nil
}
