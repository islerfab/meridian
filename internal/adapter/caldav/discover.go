package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
)

// Discovery is what a CalDAV server can tell a setup-time tool about the
// account it just authenticated.
type Discovery struct {
	// Principal is the authenticated user's principal path.
	Principal string
	// Identities are the addresses the server knows the user by, ready for
	// account.identities. Empty means the server answered without naming
	// any, which several implementations do.
	Identities []string
	// Calendars are the account's collections, whose paths a config needs.
	Calendars []DiscoveredCalendar
}

// DiscoveredCalendar is one collection as the server describes it.
type DiscoveredCalendar struct {
	Name string
	Path string
}

// Discover asks a CalDAV server who the authenticated user is and which
// calendars they own. Identities come from RFC 6638's
// calendar-user-address-set, which is the server's own statement of the
// addresses it will match against ATTENDEE — the only non-guessing answer
// available, since iCalendar has nothing like Google's self flag.
//
// The property is only meaningful on the principal, so this is two round
// trips: go-webdav finds the principal, then a hand-rolled PROPFIND asks it
// for the address set. go-webdav has no call for that property, and
// requesting it against the calendar home or the root returns 404 for it
// while still answering current-user-principal.
func Discover(ctx context.Context, cfg Config) (Discovery, error) {
	var out Discovery
	if cfg.Endpoint == "" {
		return out, fmt.Errorf("discover: endpoint is required")
	}
	httpClient := webdav.HTTPClientWithBasicAuth(&http.Client{Timeout: 30 * time.Second}, cfg.Username, cfg.Password)
	client, err := caldav.NewClient(httpClient, cfg.Endpoint)
	if err != nil {
		return out, fmt.Errorf("discover: %w", err)
	}

	out.Principal, err = client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return out, fmt.Errorf("discover: finding the current user principal: %w", err)
	}

	out.Identities, err = addressSet(ctx, httpClient, cfg.Endpoint, out.Principal)
	if err != nil {
		return out, err
	}

	// Listing calendars is a convenience alongside the address set, which is
	// what the caller came for. A server that won't enumerate them shouldn't
	// cost the caller the answer it already has.
	home, err := client.FindCalendarHomeSet(ctx, out.Principal)
	if err != nil {
		return out, nil
	}
	cals, err := client.FindCalendars(ctx, home)
	if err != nil {
		return out, nil
	}
	for _, c := range cals {
		out.Calendars = append(out.Calendars, DiscoveredCalendar{Name: c.Name, Path: c.Path})
	}
	return out, nil
}

const addressSetBody = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><C:calendar-user-address-set/></D:prop>
</D:propfind>`

func addressSet(ctx context.Context, client webdav.HTTPClient, endpoint, principal string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND",
		strings.TrimSuffix(endpoint, "/")+principal, strings.NewReader(addressSetBody))
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover: requesting calendar-user-address-set: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("discover: calendar-user-address-set: server answered %s", resp.Status)
	}

	var ms multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("discover: parsing the address set response: %w", err)
	}

	var addrs []string
	seen := map[string]bool{}
	for _, r := range ms.Responses {
		for _, ps := range r.PropStats {
			if !strings.Contains(ps.Status, " 200 ") {
				continue
			}
			for _, href := range ps.Prop.AddressSet.Hrefs {
				// The set mixes mail addresses with principal paths; only
				// the former can ever match an ATTENDEE.
				addr, ok := strings.CutPrefix(strings.TrimSpace(href), "mailto:")
				if !ok || addr == "" || seen[addr] {
					continue
				}
				seen[addr] = true
				addrs = append(addrs, addr)
			}
		}
	}
	return addrs, nil
}

type multistatus struct {
	XMLName   xml.Name      `xml:"DAV: multistatus"`
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	PropStats []davPropStat `xml:"DAV: propstat"`
}

type davPropStat struct {
	Status string  `xml:"DAV: status"`
	Prop   davProp `xml:"DAV: prop"`
}

type davProp struct {
	AddressSet davHrefs `xml:"urn:ietf:params:xml:ns:caldav calendar-user-address-set"`
}

type davHrefs struct {
	Hrefs []string `xml:"DAV: href"`
}
