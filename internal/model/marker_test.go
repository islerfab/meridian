package model

import (
	"testing"
	"time"
)

func TestEventRefRoundTrip(t *testing.T) {
	refs := []EventRef{
		{Calendar: "work/primary", UID: "4F2A9C@example.com", RecurrenceID: "20260818T120000Z"},
		{Calendar: "private/main", UID: "plain-uid", RecurrenceID: ""},
		{Calendar: "cal|with|pipes", UID: "uid%with%percent", RecurrenceID: "id|%|"},
		{Calendar: "already%7Cescaped%25", UID: "%7C", RecurrenceID: "%25"},
		{Calendar: "ünïcøde 📅", UID: "uid with spaces", RecurrenceID: ""},
		{},
	}
	for _, ref := range refs {
		got, err := ParseEventRef(ref.String())
		if err != nil {
			t.Errorf("ParseEventRef(%q): %v", ref.String(), err)
			continue
		}
		if got != ref {
			t.Errorf("round-trip %+v: encoded %q, decoded %+v", ref, ref.String(), got)
		}
	}
}

func TestEventRefEncoding(t *testing.T) {
	ref := EventRef{Calendar: "cal", UID: "uid", RecurrenceID: "20260818T120000Z"}
	if got, want := ref.String(), "cal|uid|20260818T120000Z"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	// Single events keep the trailing separator so the format stays 3-field.
	single := EventRef{Calendar: "cal", UID: "uid"}
	if got, want := single.String(), "cal|uid|"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseEventRefErrors(t *testing.T) {
	for _, s := range []string{"", "only-one", "two|fields", "a|b|c|d"} {
		if _, err := ParseEventRef(s); err == nil {
			t.Errorf("ParseEventRef(%q): expected error", s)
		}
	}
}

func testMarker() Marker {
	return NewMarker(
		"inst-test",
		EventRef{Calendar: "private/main", UID: "abc@ik.me", RecurrenceID: "20260818T120000Z"},
		"private-to-work-busy",
		ShadowContent{
			Title: "Busy",
			Start: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC),
		},
	)
}

func TestMarkerRoundTrip(t *testing.T) {
	m := testMarker()
	for _, google := range []bool{true, false} {
		props := m.Properties(google)
		got, found, err := ParseMarker(props, google)
		if err != nil || !found {
			t.Fatalf("google=%t: ParseMarker: found=%t err=%v", google, found, err)
		}
		if got != m {
			t.Errorf("google=%t: round-trip = %+v, want %+v", google, got, m)
		}
	}
}

func TestMarkerPropertyKeys(t *testing.T) {
	m := testMarker()
	gp := m.Properties(true)
	for _, k := range []string{GoogleKeySrc, GoogleKeyRule, GoogleKeyHash, GoogleKeyInstance, GoogleKeyV} {
		if _, ok := gp[k]; !ok {
			t.Errorf("google properties missing key %q (got %v)", k, gp)
		}
	}
	if gp[GoogleKeyV] != "1" {
		t.Errorf("meridian.v = %q, want \"1\"", gp[GoogleKeyV])
	}
	cp := m.Properties(false)
	for _, k := range []string{CalDAVPropSrc, CalDAVPropRule, CalDAVPropHash, CalDAVPropInstance, CalDAVPropV} {
		if _, ok := cp[k]; !ok {
			t.Errorf("caldav properties missing key %q (got %v)", k, cp)
		}
	}
}

func TestParseMarkerForeignEvent(t *testing.T) {
	for _, props := range []map[string]string{
		nil,
		{},
		{"someone.elses/key": "x", "shared": "y"},
	} {
		if _, found, err := ParseMarker(props, true); found || err != nil {
			t.Errorf("props %v: found=%t err=%v, want foreign (false, nil)", props, found, err)
		}
	}
}

func TestParseMarkerMalformed(t *testing.T) {
	valid := testMarker().Properties(true)
	mutate := func(fn func(map[string]string)) map[string]string {
		p := make(map[string]string, len(valid))
		for k, v := range valid {
			p[k] = v
		}
		fn(p)
		return p
	}
	cases := map[string]map[string]string{
		"missing hash":        mutate(func(p map[string]string) { delete(p, GoogleKeyHash) }),
		"only version":        {GoogleKeyV: "1"},
		"non-numeric version": mutate(func(p map[string]string) { p[GoogleKeyV] = "one" }),
		"future version":      mutate(func(p map[string]string) { p[GoogleKeyV] = "2" }),
		"malformed src":       mutate(func(p map[string]string) { p[GoogleKeySrc] = "no-pipes-here" }),
		"empty rule":          mutate(func(p map[string]string) { p[GoogleKeyRule] = "" }),
		"empty instance":      mutate(func(p map[string]string) { p[GoogleKeyInstance] = "" }),
		"missing instance":    mutate(func(p map[string]string) { delete(p, GoogleKeyInstance) }),
	}
	for name, props := range cases {
		_, found, err := ParseMarker(props, true)
		if !found || err == nil {
			t.Errorf("%s: found=%t err=%v, want found=true with error", name, found, err)
		}
	}
}
