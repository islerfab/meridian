// Command spike-expand is a throwaway harness for mer-pdn: verify that
// Infomaniak's sabre/dav server-side expand (RFC 4791 calendar-query REPORT
// with <expand>) behaves correctly against real recurring events, via
// go-webdav v0.7.0 CalendarExpandRequest — the exact path the CalDAV adapter
// will use (DESIGN.md Decision 3).
//
// Scenarios (dates fixed relative to Aug 2026; DST edge = Sun 2026-10-25):
//  1. weekly-exdate: weekly timed event, Europe/Zurich, one EXDATE
//  2. override:      weekly timed event with one overridden (moved) instance
//  3. allday:        weekly all-day event (must stay DATE-valued)
//  4. dst:           weekly timed event crossing the CEST→CET switch
//
// Usage (credentials from env or a gitignored .env in the repo root,
// see .env.example):
//
//	go run ./hack/spike-expand -list
//	go run ./hack/spike-expand -calendar /calendars/<user>/<id>/ -setup
//	go run ./hack/spike-expand -calendar /calendars/<user>/<id>/ -run
//	go run ./hack/spike-expand -calendar /calendars/<user>/<id>/ -cleanup
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/joho/godotenv"
)

const uidPrefix = "meridian-spike-"

var (
	windowStart = time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	windowEnd   = time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
)

const vtimezoneZurich = `BEGIN:VTIMEZONE
TZID:Europe/Zurich
BEGIN:DAYLIGHT
TZOFFSETFROM:+0100
TZOFFSETTO:+0200
TZNAME:CEST
DTSTART:19700329T020000
RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU
END:DAYLIGHT
BEGIN:STANDARD
TZOFFSETFROM:+0200
TZOFFSETTO:+0100
TZNAME:CET
DTSTART:19701025T030000
RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU
END:STANDARD
END:VTIMEZONE`

type scenario struct {
	name string // also object filename + UID suffix
	ics  string // raw VCALENDAR body
}

var scenarios = []scenario{
	{
		name: "weekly-exdate",
		// Mondays 09:00–09:45 Zurich, 6 occurrences from 2026-08-10, Aug 24 excluded.
		ics: `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//meridian//spike-expand//EN
` + vtimezoneZurich + `
BEGIN:VEVENT
UID:meridian-spike-weekly-exdate
DTSTAMP:20260807T120000Z
SUMMARY:spike weekly with EXDATE
DTSTART;TZID=Europe/Zurich:20260810T090000
DTEND;TZID=Europe/Zurich:20260810T094500
RRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=6
EXDATE;TZID=Europe/Zurich:20260824T090000
END:VEVENT
END:VCALENDAR`,
	},
	{
		name: "override",
		// Tuesdays 14:00–15:00 Zurich, 4 occurrences from 2026-08-11;
		// the Aug 18 instance is moved to Wed Aug 19 10:30–11:30.
		ics: `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//meridian//spike-expand//EN
` + vtimezoneZurich + `
BEGIN:VEVENT
UID:meridian-spike-override
DTSTAMP:20260807T120000Z
SUMMARY:spike weekly with override
DTSTART;TZID=Europe/Zurich:20260811T140000
DTEND;TZID=Europe/Zurich:20260811T150000
RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4
END:VEVENT
BEGIN:VEVENT
UID:meridian-spike-override
RECURRENCE-ID;TZID=Europe/Zurich:20260818T140000
DTSTAMP:20260807T120000Z
SUMMARY:spike weekly with override (moved)
DTSTART;TZID=Europe/Zurich:20260819T103000
DTEND;TZID=Europe/Zurich:20260819T113000
END:VEVENT
END:VCALENDAR`,
	},
	{
		name: "allday",
		// Weekly all-day event, 4 occurrences from Wed 2026-08-12.
		ics: `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//meridian//spike-expand//EN
BEGIN:VEVENT
UID:meridian-spike-allday
DTSTAMP:20260807T120000Z
SUMMARY:spike all-day recurring
DTSTART;VALUE=DATE:20260812
DTEND;VALUE=DATE:20260813
RRULE:FREQ=WEEKLY;COUNT=4
END:VEVENT
END:VCALENDAR`,
	},
	{
		name: "dst",
		// Sundays 09:00–10:00 Zurich, 4 occurrences from 2026-10-11,
		// crossing CEST→CET on 2026-10-25 (07:00Z → 08:00Z).
		ics: `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//meridian//spike-expand//EN
` + vtimezoneZurich + `
BEGIN:VEVENT
UID:meridian-spike-dst
DTSTAMP:20260807T120000Z
SUMMARY:spike DST-crossing weekly
DTSTART;TZID=Europe/Zurich:20261011T090000
DTEND;TZID=Europe/Zurich:20261011T100000
RRULE:FREQ=WEEKLY;BYDAY=SU;COUNT=4
END:VEVENT
END:VCALENDAR`,
	},
}

func main() {
	var (
		list     = flag.Bool("list", false, "discover principal + calendars, print paths")
		setup    = flag.Bool("setup", false, "PUT the four spike event objects")
		run      = flag.Bool("run", false, "run the expand REPORT and verify")
		cleanup  = flag.Bool("cleanup", false, "DELETE the four spike objects")
		calendar = flag.String("calendar", "", "calendar collection path (from -list)")
		home     = flag.String("home", "", "override calendar home set path (skip principal discovery)")
	)
	flag.Parse()

	// Real env vars win; .env (repo root, gitignored) fills the gaps.
	_ = godotenv.Load()

	endpoint := envOr("MERIDIAN_SPIKE_ENDPOINT", "https://sync.infomaniak.com")
	user := os.Getenv("MERIDIAN_SPIKE_USER")
	pass := os.Getenv("MERIDIAN_SPIKE_PASS")
	if user == "" || pass == "" {
		fatal("MERIDIAN_SPIKE_USER and MERIDIAN_SPIKE_PASS must be set (env or .env in repo root — see .env.example)")
	}

	httpClient := webdav.HTTPClientWithBasicAuth(nil, user, pass)
	client, err := caldav.NewClient(httpClient, endpoint)
	if err != nil {
		fatal("new client: %v", err)
	}
	ctx := context.Background()

	switch {
	case *list:
		doList(ctx, client, *home)
	case *setup:
		doSetup(ctx, client, requireCalendar(*calendar))
	case *run:
		doRun(ctx, client, requireCalendar(*calendar))
	case *cleanup:
		doCleanup(ctx, client, requireCalendar(*calendar))
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func doList(ctx context.Context, client *caldav.Client, homeSet string) {
	if homeSet == "" {
		principal, err := client.FindCurrentUserPrincipal(ctx)
		if err != nil {
			fatal("find principal: %v", err)
		}
		fmt.Printf("principal:    %s\n", principal)
		homeSet, err = client.FindCalendarHomeSet(ctx, principal)
		if err != nil {
			fatal("find home set: %v (retry with -home /calendars/<email>/)", err)
		}
	}
	fmt.Printf("calendar home: %s\n", homeSet)
	cals, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		fatal("find calendars: %v", err)
	}
	for _, c := range cals {
		fmt.Printf("  %-60s name=%q components=%v\n", c.Path, c.Name, c.SupportedComponentSet)
	}
}

func doSetup(ctx context.Context, client *caldav.Client, calPath string) {
	for _, sc := range scenarios {
		cal, err := ical.NewDecoder(strings.NewReader(toCRLF(sc.ics))).Decode()
		if err != nil {
			fatal("%s: parse local ICS: %v", sc.name, err)
		}
		path := objectPath(calPath, sc.name)
		if _, err := client.PutCalendarObject(ctx, path, cal); err != nil {
			fatal("%s: PUT %s: %v", sc.name, path, err)
		}
		fmt.Printf("PUT ok: %s\n", path)
	}
}

func doCleanup(ctx context.Context, client *caldav.Client, calPath string) {
	for _, sc := range scenarios {
		path := objectPath(calPath, sc.name)
		if err := client.RemoveAll(ctx, path); err != nil {
			fmt.Printf("DELETE %s: %v (continuing)\n", path, err)
			continue
		}
		fmt.Printf("DELETE ok: %s\n", path)
	}
}

// instance is one expanded VEVENT as returned by the server.
type instance struct {
	uid          string
	summary      string
	start, end   time.Time
	startRaw     string // raw DTSTART value + params, for the report
	isDate       bool   // DTSTART is DATE-valued
	hasTZID      bool   // DTSTART carries a TZID param
	recurrenceID string // raw RECURRENCE-ID value ("" if absent)
	recIDTime    time.Time
}

func doRun(ctx context.Context, client *caldav.Client, calPath string) {
	query := &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{
			Name: ical.CompCalendar,
			Comps: []caldav.CalendarCompRequest{{
				Name:     ical.CompEvent,
				AllProps: true,
			}},
			Expand: &caldav.CalendarExpandRequest{Start: windowStart, End: windowEnd},
		},
		CompFilter: caldav.CompFilter{
			Name: ical.CompCalendar,
			Comps: []caldav.CompFilter{{
				Name:  ical.CompEvent,
				Start: windowStart,
				End:   windowEnd,
			}},
		},
	}
	objs, err := client.QueryCalendar(ctx, calPath, query)
	if err != nil {
		fatal("calendar-query with expand: %v", err)
	}

	byUID := map[string][]instance{}
	for _, obj := range objs {
		for _, comp := range obj.Data.Children {
			if comp.Name != ical.CompEvent {
				continue
			}
			inst, err := parseInstance(comp)
			if err != nil {
				fmt.Printf("WARN %s: %v\n", obj.Path, err)
				continue
			}
			if !strings.HasPrefix(inst.uid, uidPrefix) {
				continue // someone else's event in this calendar
			}
			byUID[inst.uid] = append(byUID[inst.uid], inst)
		}
	}

	fmt.Printf("=== raw expanded instances (window %s .. %s) ===\n",
		windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339))
	uids := make([]string, 0, len(byUID))
	for uid := range byUID {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		insts := byUID[uid]
		sort.Slice(insts, func(i, j int) bool { return insts[i].start.Before(insts[j].start) })
		byUID[uid] = insts
		fmt.Printf("\n%s (%d instances):\n", uid, len(insts))
		for _, in := range insts {
			fmt.Printf("  DTSTART=%s  utc=%s  recurrence-id=%q  summary=%q\n",
				in.startRaw, in.start.UTC().Format(time.RFC3339), in.recurrenceID, in.summary)
		}
	}

	fmt.Printf("\n=== verification ===\n")
	pass := true
	pass = checkWeeklyExdate(byUID[uidPrefix+"weekly-exdate"]) && pass
	pass = checkOverride(byUID[uidPrefix+"override"]) && pass
	pass = checkAllDay(byUID[uidPrefix+"allday"]) && pass
	pass = checkDST(byUID[uidPrefix+"dst"]) && pass

	if pass {
		fmt.Println("\nRESULT: ALL SCENARIOS PASS — server-side expand is trustworthy")
	} else {
		fmt.Println("\nRESULT: FAILURES — see above; consider promoting client-side expansion (DESIGN.md Decision 3)")
		os.Exit(1)
	}
}

func parseInstance(comp *ical.Component) (instance, error) {
	var in instance
	in.uid, _ = comp.Props.Text(ical.PropUID)
	in.summary, _ = comp.Props.Text(ical.PropSummary)

	dtstart := comp.Props.Get(ical.PropDateTimeStart)
	if dtstart == nil {
		return in, fmt.Errorf("VEVENT %s: no DTSTART", in.uid)
	}
	in.isDate = dtstart.ValueType() == ical.ValueDate
	_, in.hasTZID = dtstart.Params[ical.ParamTimezoneID]
	in.startRaw = rawProp(dtstart)
	start, err := dtstart.DateTime(time.UTC)
	if err != nil {
		return in, fmt.Errorf("VEVENT %s: DTSTART: %w", in.uid, err)
	}
	in.start = start

	if dtend := comp.Props.Get(ical.PropDateTimeEnd); dtend != nil {
		if end, err := dtend.DateTime(time.UTC); err == nil {
			in.end = end
		}
	}
	if rid := comp.Props.Get(ical.PropRecurrenceID); rid != nil {
		in.recurrenceID = rawProp(rid)
		if t, err := rid.DateTime(time.UTC); err == nil {
			in.recIDTime = t
		}
	}
	return in, nil
}

func rawProp(p *ical.Prop) string {
	var params []string
	for k, vs := range p.Params {
		params = append(params, fmt.Sprintf(";%s=%s", k, strings.Join(vs, ",")))
	}
	sort.Strings(params)
	return strings.Join(params, "") + ":" + p.Value
}

// --- scenario checks -------------------------------------------------------

func utc(m time.Month, d, hh, mm int) time.Time {
	return time.Date(2026, m, d, hh, mm, 0, 0, time.UTC)
}

func checkStarts(name string, insts []instance, want []time.Time) bool {
	ok := true
	if len(insts) != len(want) {
		report(name, false, fmt.Sprintf("expected %d instances, got %d", len(want), len(insts)))
		ok = false
	}
	for i, w := range want {
		if i >= len(insts) {
			break
		}
		if !insts[i].start.UTC().Equal(w) {
			report(name, false, fmt.Sprintf("instance %d: expected start %s, got %s",
				i, w.Format(time.RFC3339), insts[i].start.UTC().Format(time.RFC3339)))
			ok = false
		}
	}
	return ok
}

// Expanded instances must be identifiable: RFC 4791 expand gives every
// instance of a recurring event a RECURRENCE-ID naming its original
// occurrence start — that is the stable identity Meridian's markers key on.
func checkRecurrenceIDs(name string, insts []instance) bool {
	ok := true
	seen := map[string]bool{}
	for _, in := range insts {
		if in.recurrenceID == "" {
			report(name, false, fmt.Sprintf("instance %s has no RECURRENCE-ID", in.start.UTC().Format(time.RFC3339)))
			ok = false
			continue
		}
		if seen[in.recurrenceID] {
			report(name, false, "duplicate RECURRENCE-ID "+in.recurrenceID)
			ok = false
		}
		seen[in.recurrenceID] = true
	}
	return ok
}

func checkWeeklyExdate(insts []instance) bool {
	name := "weekly-exdate"
	// Mondays 09:00 CEST = 07:00Z; 6 occurrences minus EXDATE Aug 24.
	want := []time.Time{
		utc(8, 10, 7, 0), utc(8, 17, 7, 0),
		utc(8, 31, 7, 0), utc(9, 7, 7, 0), utc(9, 14, 7, 0),
	}
	ok := checkStarts(name, insts, want)
	ok = checkRecurrenceIDs(name, insts) && ok
	for _, in := range insts {
		if in.start.UTC().Equal(utc(8, 24, 7, 0)) {
			report(name, false, "EXDATE-excluded instance Aug 24 was returned")
			ok = false
		}
	}
	report(name, ok, "5 instances, EXDATE honored, RECURRENCE-IDs unique")
	return ok
}

func checkOverride(insts []instance) bool {
	name := "override"
	// Tuesdays 14:00 CEST = 12:00Z; Aug 18 moved to Wed Aug 19 10:30 CEST = 08:30Z.
	want := []time.Time{
		utc(8, 11, 12, 0), utc(8, 19, 8, 30),
		utc(8, 25, 12, 0), utc(9, 1, 12, 0),
	}
	ok := checkStarts(name, insts, want)
	ok = checkRecurrenceIDs(name, insts) && ok
	// The moved instance must keep the ORIGINAL occurrence as its identity.
	foundMoved := false
	for _, in := range insts {
		if in.start.UTC().Equal(utc(8, 19, 8, 30)) {
			foundMoved = true
			if !in.recIDTime.UTC().Equal(utc(8, 18, 12, 0)) {
				report(name, false, fmt.Sprintf(
					"moved instance RECURRENCE-ID should reference original 2026-08-18T12:00Z, got %q", in.recurrenceID))
				ok = false
			}
		}
		if in.start.UTC().Equal(utc(8, 18, 12, 0)) {
			report(name, false, "original (superseded) Aug 18 instance still returned")
			ok = false
		}
	}
	if !foundMoved {
		report(name, false, "moved instance (Aug 19 08:30Z) missing")
		ok = false
	}
	report(name, ok, "override replaces original slot, identity points at original occurrence")
	return ok
}

func checkAllDay(insts []instance) bool {
	name := "allday"
	ok := true
	if len(insts) != 4 {
		report(name, false, fmt.Sprintf("expected 4 instances, got %d", len(insts)))
		ok = false
	}
	wantDates := []string{"20260812", "20260819", "20260826", "20260902"}
	for i, in := range insts {
		if !in.isDate {
			report(name, false, fmt.Sprintf("instance %d: DTSTART converted to DATE-TIME (%s) — all-day must stay DATE-valued", i, in.startRaw))
			ok = false
		}
		if i < len(wantDates) && !strings.HasSuffix(in.startRaw, ":"+wantDates[i]) {
			report(name, false, fmt.Sprintf("instance %d: expected DATE %s, got %s", i, wantDates[i], in.startRaw))
			ok = false
		}
	}
	ok = checkRecurrenceIDs(name, insts) && ok
	report(name, ok, "4 instances, all DATE-valued")
	return ok
}

func checkDST(insts []instance) bool {
	name := "dst"
	// Sundays 09:00 Zurich: CEST (07:00Z) until Oct 25, then CET (08:00Z).
	want := []time.Time{
		utc(10, 11, 7, 0), utc(10, 18, 7, 0),
		utc(10, 25, 8, 0), utc(11, 1, 8, 0),
	}
	ok := checkStarts(name, insts, want)
	ok = checkRecurrenceIDs(name, insts) && ok
	report(name, ok, "wall-clock 09:00 preserved across CEST→CET (07:00Z → 08:00Z)")
	return ok
}

// --- plumbing --------------------------------------------------------------

func report(name string, ok bool, msg string) {
	status := "PASS"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("[%s] %-14s %s\n", status, name, msg)
}

func objectPath(calPath, name string) string {
	return strings.TrimSuffix(calPath, "/") + "/" + uidPrefix + name + ".ics"
}

func requireCalendar(p string) string {
	if p == "" {
		fatal("-calendar is required (run -list first to find the path)")
	}
	return p
}

func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n") + "\r\n"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spike-expand: "+format+"\n", args...)
	os.Exit(1)
}
