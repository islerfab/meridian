package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runIdentitiesCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	cmd := NewRootCmd()
	cmd.Writer = &out
	err := cmd.Run(context.Background(), append([]string{"meridian", "identities"}, args...))
	return out.String(), err
}

func writeRules(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(validRules), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeCalDAV answers the two PROPFINDs discovery makes, plus the calendar
// listing, so the command can be driven end to end. addressSet is spliced in
// so a test can model a server that reports no addresses.
func fakeCalDAV(t *testing.T) *httptest.Server {
	return fakeCalDAVWith(t, `<cal:calendar-user-address-set><d:href>mailto:me@example.com</d:href></cal:calendar-user-address-set>`)
}

func fakeCalDAVWith(t *testing.T, addressSet string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := string(body)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)

		switch {
		case strings.Contains(req, "calendar-user-address-set"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
 <d:response><d:href>/principals/u/</d:href><d:propstat>
  <d:prop>`+addressSet+`</d:prop>
  <d:status>HTTP/1.1 200 OK</d:status>
 </d:propstat></d:response></d:multistatus>`)

		case strings.Contains(req, "calendar-home-set"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
 <d:response><d:href>/principals/u/</d:href><d:propstat>
  <d:prop><cal:calendar-home-set><d:href>/calendars/u/</d:href></cal:calendar-home-set></d:prop>
  <d:status>HTTP/1.1 200 OK</d:status>
 </d:propstat></d:response></d:multistatus>`)

		case strings.Contains(req, "resourcetype"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
 <d:response><d:href>/calendars/u/personal/</d:href><d:propstat>
  <d:prop>
   <d:resourcetype><d:collection/><cal:calendar/></d:resourcetype>
   <d:displayname>Personal</d:displayname>
   <cal:supported-calendar-component-set><cal:comp name="VEVENT"/></cal:supported-calendar-component-set>
  </d:prop>
  <d:status>HTTP/1.1 200 OK</d:status>
 </d:propstat></d:response></d:multistatus>`)

		default:
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
 <d:response><d:href>/</d:href><d:propstat>
  <d:prop><d:current-user-principal><d:href>/principals/u/</d:href></d:current-user-principal></d:prop>
  <d:status>HTTP/1.1 200 OK</d:status>
 </d:propstat></d:response></d:multistatus>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The command exists to be run while writing the config it helps fill in,
// so it has to work with no config file present at all.
func TestIdentitiesWithoutAConfig(t *testing.T) {
	srv := fakeCalDAV(t)
	t.Setenv("MERIDIAN_CALDAV_USERNAME", "u")
	t.Setenv("MERIDIAN_CALDAV_PASSWORD", "p")

	out, err := runIdentitiesCmd(t, "--endpoint", srv.URL)
	if err != nil {
		t.Fatalf("bootstrap run failed: %v", err)
	}
	if !strings.Contains(out, "identities: [me@example.com]") {
		t.Errorf("output missing the paste-ready line:\n%s", out)
	}
	if !strings.Contains(out, "/principals/u/") {
		t.Errorf("output missing the principal:\n%s", out)
	}
}

func TestIdentitiesBootstrapNeedsCredentials(t *testing.T) {
	srv := fakeCalDAV(t)
	t.Setenv("MERIDIAN_CALDAV_USERNAME", "")
	t.Setenv("MERIDIAN_CALDAV_PASSWORD", "")

	_, err := runIdentitiesCmd(t, "--endpoint", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("want a credentials error, got %v", err)
	}
}

func TestIdentitiesRejectsAccountAndEndpointTogether(t *testing.T) {
	_, err := runIdentitiesCmd(t, "--config", writeRules(t), "work", "--endpoint", "https://x")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("want a mutually-exclusive error, got %v", err)
	}
}

func TestIdentitiesNeedsAnAccountOrEndpoint(t *testing.T) {
	t.Setenv("MERIDIAN_CALDAV_ENDPOINT", "")
	_, err := runIdentitiesCmd(t, "--config", writeRules(t))
	if err == nil || !strings.Contains(err.Error(), "--endpoint") {
		t.Fatalf("want a guidance error, got %v", err)
	}
}

func TestIdentitiesRejectsTooManyArguments(t *testing.T) {
	if _, err := runIdentitiesCmd(t, "--config", writeRules(t), "work", "extra"); err == nil {
		t.Error("want a usage error with two accounts named")
	}
}

func TestIdentitiesRejectsUnknownAccount(t *testing.T) {
	_, err := runIdentitiesCmd(t, "--config", writeRules(t), "nope")
	if err == nil || !strings.Contains(err.Error(), "not a configured account") {
		t.Fatalf("want an unknown-account error, got %v", err)
	}
}

// Google accounts need no identities at all, so asking for them is a
// mistake worth naming rather than an empty result.
func TestIdentitiesRejectsGoogleAccount(t *testing.T) {
	_, err := runIdentitiesCmd(t, "--config", writeRules(t), "personal")
	if err == nil || !strings.Contains(err.Error(), "not caldav") {
		t.Fatalf("want a not-caldav error, got %v", err)
	}
}

func TestIdentitiesReportsMissingCredentials(t *testing.T) {
	t.Setenv("U", "")
	_, err := runIdentitiesCmd(t, "--config", writeRules(t), "work")
	if err == nil || !strings.Contains(err.Error(), "U") {
		t.Fatalf("want an error naming the empty env var, got %v", err)
	}
}

// A server that doesn't implement the property must still leave the user
// with something to paste, rather than an empty answer and no guidance.
func TestIdentitiesGuidesWhenServerReportsNoAddresses(t *testing.T) {
	srv := fakeCalDAVWith(t, ``)
	t.Setenv("MERIDIAN_CALDAV_USERNAME", "u")
	t.Setenv("MERIDIAN_CALDAV_PASSWORD", "p")

	out, err := runIdentitiesCmd(t, "--endpoint", srv.URL)
	if err != nil {
		t.Fatalf("an unsupported property is not a failure: %v", err)
	}
	if !strings.Contains(out, "did not report any addresses") {
		t.Errorf("output should explain the empty result:\n%s", out)
	}
	if !strings.Contains(out, "identities: [you@example.com]") {
		t.Errorf("output should still offer a placeholder to edit:\n%s", out)
	}
}

// The server's own label for a calendar is not the name a rule uses, and
// the output has to be explicit about which identifier goes where.
func TestIdentitiesExplainsWhichIdentifierToUse(t *testing.T) {
	srv := fakeCalDAV(t)
	t.Setenv("MERIDIAN_CALDAV_USERNAME", "u")
	t.Setenv("MERIDIAN_CALDAV_PASSWORD", "p")

	out, err := runIdentitiesCmd(t, "--endpoint", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "NAME ON THE SERVER") {
		t.Errorf("table should say whose name the left column is:\n%s", out)
	}
	if !strings.Contains(out, "`name` is yours to choose") {
		t.Errorf("output should say the config name is the operator's:\n%s", out)
	}
}
