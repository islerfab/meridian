package google

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	calendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// TestResolveCalendarID_AliasReturnsRealID guards the sweep-deletes-primary
// bug: CalendarList.List (Discover) never returns the literal "primary"
// alias for the primary calendar, only its real ID. ResolveCalendarID must
// return that same real ID so the sweep's targeted-calendar map can match it.
func TestResolveCalendarID_AliasReturnsRealID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/calendars/primary" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(&calendar.Calendar{Id: "someone@gmail.com"})
	}))
	defer srv.Close()

	svc, err := calendar.NewService(t.Context(), option.WithoutAuthentication(), option.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	a := &Account{svc: svc, cfg: AccountConfig{AccountName: "private"}}

	got, err := a.ResolveCalendarID(t.Context(), "primary")
	if err != nil {
		t.Fatalf("ResolveCalendarID: %v", err)
	}
	if got != "someone@gmail.com" {
		t.Errorf("got %q, want %q (the alias must resolve to the real ID, not pass through unchanged)", got, "someone@gmail.com")
	}
}
