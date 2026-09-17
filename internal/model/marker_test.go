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
	m.RepairTries = 2 // non-zero must survive the round trip too
	for _, p := range []Protocol{ProtocolGoogle, ProtocolCalDAV} {
		props := m.Properties(p)
		got, found, err := ParseMarker(props, p)
		if err != nil || !found {
			t.Fatalf("protocol=%v: ParseMarker: found=%t err=%v", p, found, err)
		}
		if got != m {
			t.Errorf("protocol=%v: round-trip = %+v, want %+v", p, got, m)
		}
	}
}

func TestMarkerPropertyKeys(t *testing.T) {
	m := testMarker()
	gp := m.Properties(ProtocolGoogle)
	for _, k := range []string{GoogleKeySrc, GoogleKeyRule, GoogleKeyHash, GoogleKeyInstance, GoogleKeyRepair, GoogleKeyV} {
		if _, ok := gp[k]; !ok {
			t.Errorf("google properties missing key %q (got %v)", k, gp)
		}
	}
	if gp[GoogleKeyV] != "2" {
		t.Errorf("meridian.v = %q, want \"2\"", gp[GoogleKeyV])
	}
	if gp[GoogleKeyRepair] != "0" {
		t.Errorf("meridian.repair = %q, want \"0\"", gp[GoogleKeyRepair])
	}
	cp := m.Properties(ProtocolCalDAV)
	for _, k := range []string{CalDAVPropSrc, CalDAVPropRule, CalDAVPropHash, CalDAVPropInstance, CalDAVPropRepair, CalDAVPropV} {
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
		if _, found, err := ParseMarker(props, ProtocolGoogle); found || err != nil {
			t.Errorf("props %v: found=%t err=%v, want foreign (false, nil)", props, found, err)
		}
	}
}

// Backward-compatible reads: a v1 marker never had the repair key at all —
// not missing, predates the concept — and must still decode cleanly, with
// RepairTries defaulting to the historically correct 0.
func TestParseMarkerV1BackwardCompat(t *testing.T) {
	props := map[string]string{
		GoogleKeySrc:      "cal|uid|",
		GoogleKeyRule:     "some-rule",
		GoogleKeyHash:     "deadbeef",
		GoogleKeyInstance: "inst-test",
		GoogleKeyV:        "1",
	}
	got, found, err := ParseMarker(props, ProtocolGoogle)
	if err != nil || !found {
		t.Fatalf("v1 marker: found=%t err=%v", found, err)
	}
	want := Marker{
		Src:      EventRef{Calendar: "cal", UID: "uid"},
		Rule:     "some-rule",
		Hash:     "deadbeef",
		Instance: "inst-test",
		V:        1,
	}
	if got != want {
		t.Errorf("v1 marker = %+v, want %+v", got, want)
	}
}

// A version this binary predates (or no longer supports) must still error
// loudly rather than silently misinterpreting an unknown shape — the
// wipe/migrate fallback is for exactly this case.
func TestParseMarkerUnsupportedVersion(t *testing.T) {
	for _, v := range []string{"0", "3", "99"} {
		props := map[string]string{
			GoogleKeySrc: "cal|uid|", GoogleKeyRule: "r", GoogleKeyHash: "h",
			GoogleKeyInstance: "i", GoogleKeyV: v,
		}
		if _, found, err := ParseMarker(props, ProtocolGoogle); !found || err == nil {
			t.Errorf("version %s: found=%t err=%v, want found=true with error", v, found, err)
		}
	}
}

func TestParseMarkerMalformed(t *testing.T) {
	valid := testMarker().Properties(ProtocolGoogle)
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
		"only version":        {GoogleKeyV: "2"},
		"non-numeric version": mutate(func(p map[string]string) { p[GoogleKeyV] = "one" }),
		"future version":      mutate(func(p map[string]string) { p[GoogleKeyV] = "3" }),
		"malformed src":       mutate(func(p map[string]string) { p[GoogleKeySrc] = "no-pipes-here" }),
		"empty rule":          mutate(func(p map[string]string) { p[GoogleKeyRule] = "" }),
		"empty instance":      mutate(func(p map[string]string) { p[GoogleKeyInstance] = "" }),
		"missing instance":    mutate(func(p map[string]string) { delete(p, GoogleKeyInstance) }),
		"missing repair":      mutate(func(p map[string]string) { delete(p, GoogleKeyRepair) }),
		"non-numeric repair":  mutate(func(p map[string]string) { p[GoogleKeyRepair] = "one" }),
	}
	for name, props := range cases {
		_, found, err := ParseMarker(props, ProtocolGoogle)
		if !found || err == nil {
			t.Errorf("%s: found=%t err=%v, want found=true with error", name, found, err)
		}
	}
}
