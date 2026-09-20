package caldav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const principalPath = "/principals/u/"

// discoverServer answers the two PROPFINDs Discover makes: the root one that
// resolves the principal, and the one against the principal that asks for
// the address set. addressSetXML is spliced in so a test can model a server
// that answers without naming any address.
func discoverServer(t *testing.T, addressSetXML string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			http.Error(w, "unexpected method "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)

		switch {
		case strings.Contains(string(body), "calendar-user-address-set"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
 <d:response><d:href>`+principalPath+`</d:href><d:propstat>
  <d:prop>`+addressSetXML+`</d:prop>
  <d:status>HTTP/1.1 200 OK</d:status>
 </d:propstat></d:response></d:multistatus>`)
		default:
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
 <d:response><d:href>/</d:href><d:propstat>
  <d:prop><d:current-user-principal><d:href>`+principalPath+`</d:href></d:current-user-principal></d:prop>
  <d:status>HTTP/1.1 200 OK</d:status>
 </d:propstat></d:response></d:multistatus>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscoverReadsAddressSet(t *testing.T) {
	// A real server mixes mail addresses with principal hrefs in the set;
	// only the former can ever match an ATTENDEE.
	srv := discoverServer(t, `<cal:calendar-user-address-set>
   <d:href>mailto:me@example.com</d:href>
   <d:href>`+principalPath+`</d:href>
   <d:href>mailto:alias@example.com</d:href>
  </cal:calendar-user-address-set>`)

	got, err := Discover(context.Background(), Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Principal != principalPath {
		t.Errorf("principal = %q, want %q", got.Principal, principalPath)
	}
	want := []string{"me@example.com", "alias@example.com"}
	if len(got.Identities) != len(want) {
		t.Fatalf("identities = %v, want %v", got.Identities, want)
	}
	for i := range want {
		if got.Identities[i] != want[i] {
			t.Errorf("identities = %v, want %v", got.Identities, want)
		}
	}
}

// Servers that don't implement the property answer without it. That must
// report nothing rather than inventing an address.
func TestDiscoverWithoutAddressSetSupport(t *testing.T) {
	srv := discoverServer(t, ``)
	got, err := Discover(context.Background(), Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("an unsupported property is not an error: %v", err)
	}
	if len(got.Identities) != 0 {
		t.Errorf("identities = %v, want none", got.Identities)
	}
}

func TestDiscoverDeduplicates(t *testing.T) {
	srv := discoverServer(t, `<cal:calendar-user-address-set>
   <d:href>mailto:me@example.com</d:href>
   <d:href>mailto:me@example.com</d:href>
  </cal:calendar-user-address-set>`)
	got, err := Discover(context.Background(), Config{Endpoint: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Identities) != 1 {
		t.Errorf("identities = %v, want one entry", got.Identities)
	}
}

func TestDiscoverRequiresEndpoint(t *testing.T) {
	if _, err := Discover(context.Background(), Config{}); err == nil {
		t.Error("want an error for an empty endpoint")
	}
}
