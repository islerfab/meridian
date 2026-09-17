package caldav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{fmt.Errorf("404 Not Found: <error>sabre stuff</error>"), adapter.ErrNotFound},
		{fmt.Errorf("410 Gone"), adapter.ErrNotFound},
		{fmt.Errorf("401 Unauthorized"), adapter.ErrAuthFailed},
		{fmt.Errorf("403 Forbidden"), adapter.ErrAuthFailed},
		{fmt.Errorf("429 Too Many Requests"), adapter.ErrRateLimited},
		{fmt.Errorf("500 Internal Server Error"), adapter.ErrTransient},
		{fmt.Errorf("503 Service Unavailable"), adapter.ErrTransient},
		{fmt.Errorf("wrapped: %w", fmt.Errorf("404 Not Found")), adapter.ErrNotFound},
		{errors.New("dial tcp: connection refused"), adapter.ErrTransient},
	}
	for _, c := range cases {
		if got := classify(c.err); !errors.Is(got, c.want) {
			t.Errorf("classify(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func newTestAdapter(t *testing.T, handler http.HandlerFunc) *Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := New(Config{
		Endpoint:     srv.URL,
		Username:     "u",
		Password:     "p",
		CalendarPath: "/calendars/u/test/",
		CalendarID:   "test",
		InstanceID:   "inst-test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Deletes are idempotent: already-gone (404/410) is success.
func TestDeleteIdempotent(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", status)
		})
		err := a.Delete(context.Background(), model.ShadowRef{Calendar: "test", ID: "/calendars/u/test/x.ics"})
		if err != nil {
			t.Errorf("status %d: Delete = %v, want nil", status, err)
		}
	}
}

func TestDeleteOtherErrorsPropagate(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	})
	err := a.Delete(context.Background(), model.ShadowRef{Calendar: "test", ID: "/calendars/u/test/x.ics"})
	if !errors.Is(err, adapter.ErrAuthFailed) {
		t.Errorf("Delete = %v, want ErrAuthFailed", err)
	}
}

func TestListEventsAuthFailure(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	_, err := a.ListEvents(context.Background(), adapter.Window{})
	if !errors.Is(err, adapter.ErrAuthFailed) {
		t.Errorf("ListEvents = %v, want ErrAuthFailed", err)
	}
}

func TestWindowOverlaps(t *testing.T) {
	w := adapter.Window{
		Start: mustTime(t, "2026-08-09T00:00:00Z"),
		End:   mustTime(t, "2026-11-08T00:00:00Z"),
	}
	cases := []struct {
		name       string
		start, end string
		want       bool
	}{
		{"inside", "2026-09-01T10:00:00Z", "2026-09-01T11:00:00Z", true},
		{"straddles start", "2026-08-08T23:00:00Z", "2026-08-09T01:00:00Z", true},
		{"ends at window start (exclusive)", "2026-08-08T22:00:00Z", "2026-08-09T00:00:00Z", false},
		{"starts at window end", "2026-11-08T00:00:00Z", "2026-11-08T01:00:00Z", false},
		{"before", "2026-08-01T00:00:00Z", "2026-08-02T00:00:00Z", false},
		{"zero-length inside", "2026-09-01T10:00:00Z", "2026-09-01T10:00:00Z", true},
		{"zero-length at window start", "2026-08-09T00:00:00Z", "2026-08-09T00:00:00Z", true},
	}
	for _, c := range cases {
		if got := w.Overlaps(mustTime(t, c.start), mustTime(t, c.end)); got != c.want {
			t.Errorf("%s: Overlaps = %t, want %t", c.name, got, c.want)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
