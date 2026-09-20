package caldav

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		// httpStatusRe is anchored: a status-shaped number inside a
		// sentence must not be read as the response status, or an
		// unrelated failure starts counting as a tombstone.
		{errors.New("expected 404 objects in the collection"), adapter.ErrTransient},
		{&url.Error{Op: "PROPFIND", URL: "https://dav.example.com/", Err: errors.New("EOF")}, adapter.ErrTransient},
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

// multistatusWith renders a REPORT response carrying the given VEVENT
// bodies, one calendar object each.
func multistatusWith(t *testing.T, vevents ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">`)
	for i, ve := range vevents {
		ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//test//EN\r\n" +
			strings.ReplaceAll(ve, "\n", "\r\n") + "\r\nEND:VCALENDAR\r\n"
		fmt.Fprintf(&b, `<D:response><D:href>/calendars/u/test/obj-%d.ics</D:href>`+
			`<D:propstat><D:prop><C:calendar-data>%s</C:calendar-data></D:prop>`+
			`<D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`,
			i, html.EscapeString(ics))
	}
	b.WriteString(`</D:multistatus>`)
	return b.String()
}

func reportHandler(t *testing.T, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}
}

// A cancelled event occupies no time, so mirroring one writes a shadow that
// blocks a slot nobody is using. Google's adapter has always dropped these;
// this asserts CalDAV does too, because an engine that can tell the two
// protocols apart is the thing the adapter contract exists to prevent.
func TestListEventsSkipsCancelled(t *testing.T) {
	body := multistatusWith(t,
		"BEGIN:VEVENT\nUID:live@test\nDTSTART:20260910T090000Z\nDTEND:20260910T100000Z\nSUMMARY:Live\nEND:VEVENT",
		"BEGIN:VEVENT\nUID:gone@test\nDTSTART:20260910T110000Z\nDTEND:20260910T120000Z\nSUMMARY:Cancelled\nSTATUS:CANCELLED\nEND:VEVENT",
	)
	a := newTestAdapter(t, reportHandler(t, body))

	events, err := a.ListEvents(context.Background(), adapter.Window{
		Start: mustTime(t, "2026-09-01T00:00:00Z"),
		End:   mustTime(t, "2026-10-01T00:00:00Z"),
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (the cancelled one must be dropped): %+v", len(events), events)
	}
	if events[0].Ref.UID != "live@test" {
		t.Errorf("kept %q, want live@test", events[0].Ref.UID)
	}
}

// Tentative is not cancelled: it still blocks time, and filter.when can
// select on it. Dropping it would be over-reach.
func TestListEventsKeepsTentative(t *testing.T) {
	body := multistatusWith(t,
		"BEGIN:VEVENT\nUID:maybe@test\nDTSTART:20260910T090000Z\nDTEND:20260910T100000Z\nSUMMARY:Maybe\nSTATUS:TENTATIVE\nEND:VEVENT",
	)
	a := newTestAdapter(t, reportHandler(t, body))

	events, err := a.ListEvents(context.Background(), adapter.Window{
		Start: mustTime(t, "2026-09-01T00:00:00Z"),
		End:   mustTime(t, "2026-10-01T00:00:00Z"),
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Status != model.StatusTentative {
		t.Fatalf("got %+v, want one tentative event", events)
	}
}
