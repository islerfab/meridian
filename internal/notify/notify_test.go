package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNopSwallowsEverything(t *testing.T) {
	if err := (Nop{}).Notify(context.Background(), "anything"); err != nil {
		t.Errorf("Nop.Notify = %v, want nil", err)
	}
}

// The body shape is a published contract, not an internal detail: a
// renamed key still delivers a 2xx, which is all this code ever sees, so
// the failure is silent at the far end. "content" is also what makes a
// bare Discord URL work with no receiver in between.
func TestWebhookPostsContentAsJSON(t *testing.T) {
	var (
		gotMethod string
		gotType   string
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	wh := &Webhook{URL: srv.URL, Client: srv.Client()}
	if err := wh.Notify(context.Background(), "guard: mass delete on work/main"); err != nil {
		t.Fatalf("Notify = %v, want nil", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("body %q is not JSON: %v", gotBody, err)
	}
	if payload["content"] != "guard: mass delete on work/main" {
		t.Errorf("content = %q, want the message verbatim", payload["content"])
	}
}

// A message carrying quotes, newlines and backslashes must survive intact —
// guard notifications embed rule IDs and calendar names.
func TestWebhookEscapesMessage(t *testing.T) {
	const msg = "rule \"a\\b\" deleted 4/4 shadows\non work/main"
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		got = payload["content"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := &Webhook{URL: srv.URL, Client: srv.Client()}
	if err := wh.Notify(context.Background(), msg); err != nil {
		t.Fatalf("Notify = %v, want nil", err)
	}
	if got != msg {
		t.Errorf("content = %q, want %q", got, msg)
	}
}

func TestWebhookNon2xxIsAnError(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		wh := &Webhook{URL: srv.URL, Client: srv.Client()}
		err := wh.Notify(context.Background(), "x")
		srv.Close()
		if err == nil {
			t.Errorf("status %d: Notify = nil, want an error", code)
			continue
		}
		if !strings.Contains(err.Error(), "notify webhook") {
			t.Errorf("status %d: error %q does not name the channel", code, err)
		}
	}
}

func TestWebhookUnreachableIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	wh := &Webhook{URL: url}
	if err := wh.Notify(context.Background(), "x"); err == nil {
		t.Error("Notify = nil, want an error for an unreachable webhook")
	}
}

// A cancelled context must not be reported as success: the engine counts
// notify failures, and a silent one would hide a channel that never
// delivers.
func TestWebhookCancelledContextIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	wh := &Webhook{URL: srv.URL, Client: srv.Client()}
	if err := wh.Notify(ctx, "x"); err == nil {
		t.Error("Notify = nil, want an error for a cancelled context")
	}
}
