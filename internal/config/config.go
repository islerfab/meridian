package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/islerfab/meridian/internal/model"
)

// Config is the root of rules.yaml: instance identity, sync interval,
// operator notifications, provider accounts, and sync rules. Parsing is
// strict — unknown fields are startup errors — and secrets never appear
// here: account fields name environment variable identifiers, resolved at
// startup from wherever the deployment sources them (a Kubernetes Secret,
// .env locally).
type Config struct {
	// Instance is this Meridian instance's identity, embedded in every
	// ownership marker it writes. Must be unique across any instances
	// sharing a destination, and stable for the instance's lifetime —
	// changing it orphans every shadow the instance previously wrote (they
	// become invisible to it; `meridian wipe calendar <calendar> --instance
	// <old>` cleans them up).
	Instance string `yaml:"instance" doc:"required"`
	// Interval is the time between reconciliation cycles.
	Interval string `yaml:"interval" doc:"optional,default=5m"`

	// Notifications is the operator notification channel. Leaving it unset
	// disables delivery without disabling the metrics and logs covering the
	// same events, so it is redundant wherever those are already alerted
	// on, and the only out-of-band signal where they are not.
	Notifications Notifications `yaml:"notifications"`
	// Guards tunes the hardening guards. They are on by default and work
	// whether or not notifications are configured.
	Guards Guards `yaml:"guards"`
	// Accounts is the set of provider connections rules draw their
	// calendars from.
	Accounts []Account `yaml:"accounts"`
	// Rules is the set of one-way mirrors this instance reconciles. At
	// least one is required.
	Rules []RuleConfig `yaml:"rules"`

	// IntervalDuration is the parsed Interval (set by Load).
	IntervalDuration time.Duration `yaml:"-"`
}

// Notifications configures the operator notification channel.
type Notifications struct {
	// WebhookURLEnv names the env var holding a webhook URL to POST
	// notifications to, as `{"content": "..."}` — Discord's incoming-webhook
	// shape, so one of those URLs needs nothing in front of it.
	// Empty/unset disables notifications, which is the normal setup under
	// Kubernetes: guard triggers and auth failures emit metrics and logs
	// either way, and those are what an alerting stack is already watching.
	WebhookURLEnv string `yaml:"webhookURLEnv" doc:"optional"`
}

// Guards tunes the hardening guards. Everything here governs detection, not
// blocking: a guard records what it saw and the cycle proceeds.
type Guards struct {
	// MassDeleteFraction triggers the mass-delete guard
	// (meridian_guard_triggers_total{guard="mass_delete"}) when a rule
	// deletes more than this fraction of its shadows in one cycle. The
	// trigger also sends a notification when one is configured, but the
	// metric and the log fire either way. 0 disables the guard.
	MassDeleteFraction *float64 `yaml:"massDeleteFraction" doc:"optional,default=0.5"`
}

// Account is one provider connection. Type-specific fields are validated
// per type; setting a field that belongs to the other type is a startup
// error.
type Account struct {
	// Name is the account's unique name, referenced in rules as
	// `<account>/<calendar>`.
	Name string `yaml:"name" doc:"required"`
	// Type selects the provider: google or caldav.
	Type string `yaml:"type" doc:"required"`

	// Endpoint is the CalDAV server's base URL.
	Endpoint string `yaml:"endpoint" doc:"required,caldav"`
	// UsernameEnv names the env var holding the CalDAV Basic Auth username.
	UsernameEnv string `yaml:"usernameEnv" doc:"required,caldav"`
	// PasswordEnv names the env var holding the CalDAV Basic Auth password
	// (an app-specific password, if the provider requires 2FA).
	PasswordEnv string `yaml:"passwordEnv" doc:"required,caldav"`
	// ClientIDEnv names the env var holding the Google OAuth client ID.
	ClientIDEnv string `yaml:"clientIDEnv" doc:"required,google"`
	// ClientSecretEnv names the env var holding the Google OAuth client
	// secret.
	ClientSecretEnv string `yaml:"clientSecretEnv" doc:"required,google"`
	// RefreshTokenEnv names the env var holding a long-lived Google OAuth
	// refresh token. Get one via `meridian oauth`.
	RefreshTokenEnv string `yaml:"refreshTokenEnv" doc:"required,google"`

	// Identities are the calendar addresses this server knows the account
	// owner by, used to read the owner's own RSVP off an invitation.
	// Google needs none — the API marks the owner's attendee entry — so
	// this is CalDAV-only, where iCalendar has no such marker. Usually one
	// address; `meridian identities <account>` asks the server for it.
	//
	// Only addresses belonging to THIS account. A calendar mirrored in from
	// elsewhere carries the owner's address on that other system, which the
	// server won't report; add it here by hand to read RSVP off such a
	// calendar.
	Identities []string `yaml:"identities" doc:"optional,caldav"`

	// Calendars is the set of this account's calendars that rules may
	// reference. At least one is required per account.
	Calendars []CalendarConfig `yaml:"calendars"`
}

// CalendarConfig names one calendar of an account. The logical reference
// used in rules is `<account>/<calendar>`.
type CalendarConfig struct {
	// Name is unique within the account; combines with the account name
	// as `<account>/<name>` in rules.
	Name string `yaml:"name" doc:"required"`
	// Path is the CalDAV collection path.
	Path string `yaml:"path" doc:"required,caldav"`
	// ID is the Google Calendar ID: "primary", or
	// "xxxx@group.calendar.google.com" from Settings -> Integrate calendar.
	ID string `yaml:"id" doc:"required,google"`
}

// RuleConfig is one sync rule (compiled into sync.Rule). A destination
// calendar can be targeted by any number of rules simultaneously —
// ownership is scoped by instance + rule ID, so rules never fight over
// each other's shadows.
type RuleConfig struct {
	// ID is unique across the whole config, embedded in every shadow's
	// ownership marker. Renaming a rule is equivalent to deleting it and
	// creating a new one: old shadows get swept as orphans, new ones get
	// created fresh.
	ID string `yaml:"id" doc:"required"`
	// From is the source `<account>/<calendar>` reference; must be a
	// configured calendar.
	From string `yaml:"from" doc:"required"`
	// To lists destination `<account>/<calendar>` references; none may
	// equal From.
	To []string `yaml:"to" doc:"required"`
	// Filter selects which source events this rule mirrors. Unset matches
	// every event in the sync window.
	Filter *FilterConfig `yaml:"filter" doc:"optional"`
	// Transform computes the shadow's content. Unset mirrors every field
	// faithfully.
	Transform *TransformConfig `yaml:"transform" doc:"optional"`
}

// FilterConfig selects source events. All set fields AND together: an
// event must satisfy every one to match.
type FilterConfig struct {
	// Weekdays is a subset of mon..sun. All-day events are matched by
	// their date against this list alone — never subject to Window.
	Weekdays []string `yaml:"weekdays" doc:"optional"`
	// Window is a daily time-of-day range ("HH:MM-HH:MM", must not cross
	// midnight), evaluated in Timezone. An event matches if it overlaps
	// the window on a matching weekday.
	Window string `yaml:"window" doc:"optional"`
	// Timezone is an IANA zone name, required whenever Window or Weekdays
	// is set — event times are UTC instants internally, so this is what
	// makes the wall-clock window meaningful.
	Timezone string `yaml:"timezone" doc:"optional"`
	// SkipTransparent skips events marked transparent ("free") at the
	// source.
	SkipTransparent bool `yaml:"skipTransparent" doc:"optional,default=false"`
	// SkipAllDay skips all-day events entirely.
	SkipAllDay bool `yaml:"skipAllDay" doc:"optional,default=false"`
	// SkipDeclined skips invitations the calendar owner declined. Events
	// the owner was never invited to are unaffected, so this never touches
	// ordinary non-meeting events. CalDAV sources need account.identities
	// for the owner's response to be readable at all.
	SkipDeclined bool `yaml:"skipDeclined" doc:"optional,default=false"`
	// When is a CEL expression over the CELEvent schema, ANDed with every
	// other set field. Compiled and type-checked at config load — a bad
	// expression is a startup error, never a mid-sync surprise.
	When string `yaml:"when" doc:"optional"`
}

// TransformConfig computes the shadow's content. Every field left unset
// copies the corresponding source value (faithful mirror by default); it
// only needs to name what should differ. Deliberately not
// expression-powered the way filter.when is: a bad filter mis-selects
// events, but a bad transform corrupts calendar data, so string fields get
// templating and nothing more.
type TransformConfig struct {
	// Title is a literal string or a Go template
	// (https://pkg.go.dev/text/template) over the source event; the
	// literal "drop" empties it instead of copying or templating it.
	Title *string `yaml:"title" doc:"optional"`
	// Description follows the same rules as Title.
	Description *string `yaml:"description" doc:"optional"`
	// Location follows the same rules as Title.
	Location *string `yaml:"location" doc:"optional"`
	// Transparent forces the shadow's opacity regardless of the source
	// event's own transparency; unset copies the source's value.
	Transparent *bool `yaml:"transparent" doc:"optional"`
	// TransparentForRSVP lists the responses (needsAction, accepted,
	// declined, tentative) whose copies stop blocking time, so a meeting
	// still sitting unanswered shows up without holding the slot. Any other
	// response keeps the source's own transparency, and an event the owner
	// was never invited to is untouched. Mutually exclusive with
	// `transparent`, which forces one answer for every event.
	TransparentForRSVP []string `yaml:"transparentForRSVP" doc:"optional"`
	// Reminders is a list of minutes before start; an empty list means no
	// reminders. Unset copies the source's reminders.
	Reminders []int `yaml:"reminders" doc:"optional"`
	// Color has no source value to copy, so unset means no override at
	// all, not "mirror the source's color". Encoding is
	// destination-provider-specific — see the rules cookbook.
	Color *string `yaml:"color" doc:"optional"`
	// Visibility sets who may read the shadow: public, private or
	// confidential. Unset mirrors the source. Independent of Transparent,
	// which governs whether the shadow blocks time rather than who sees it.
	// Not templated: a closed set is worth more here than an expression
	// free to produce a value no provider accepts.
	Visibility *string `yaml:"visibility" doc:"optional"`
}

// Drop is the transform keyword that empties a string field.
const Drop = "drop"

// visibilityNames is the closed set transform.visibility accepts. Declared
// as a literal so the chart's values.schema.json is generated from it and
// cannot drift from what this validator enforces.
var visibilityNames = map[string]model.Visibility{
	"public":       model.VisibilityPublic,
	"private":      model.VisibilityPrivate,
	"confidential": model.VisibilityConfidential,
}

// rsvpNames is the closed set transform.transparentForRSVP accepts, kept as
// a literal for the same reason visibilityNames is: the chart's generated
// schema and this validator must be the same list.
var rsvpNames = map[string]model.RSVP{
	"needsAction": model.RSVPNeedsAction,
	"accepted":    model.RSVPAccepted,
	"declined":    model.RSVPDeclined,
	"tentative":   model.RSVPTentative,
}

var weekdayNames = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

var windowRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])-([01][0-9]|2[0-3]):([0-5][0-9])$`)

// Load reads, strictly parses, and validates the config file. It does NOT
// resolve secret env vars (BuildAdapters does) but does validate structure,
// references, windows, CEL, and templates — all failures are startup errors.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Instance == "" {
		fail("instance is required (unique per meridian instance, stable — changing it orphans all shadows)")
	}
	c.IntervalDuration = 5 * time.Minute
	if c.Interval != "" {
		d, err := time.ParseDuration(c.Interval)
		if err != nil || d <= 0 {
			fail("interval %q: must be a positive Go duration", c.Interval)
		} else {
			c.IntervalDuration = d
		}
	}
	if c.Guards.MassDeleteFraction == nil {
		half := 0.5
		c.Guards.MassDeleteFraction = &half
	} else if f := *c.Guards.MassDeleteFraction; f < 0 || f > 1 {
		fail("guards.massDeleteFraction %v: must be in [0,1]", f)
	}

	calendars := map[string]bool{} // "<account>/<calendar>"
	accountNames := map[string]bool{}
	for i := range c.Accounts {
		a := &c.Accounts[i]
		where := fmt.Sprintf("accounts[%d] (%s)", i, a.Name)
		if a.Name == "" {
			fail("%s: name is required", where)
		}
		if accountNames[a.Name] {
			fail("%s: duplicate account name", where)
		}
		accountNames[a.Name] = true
		if len(a.Calendars) == 0 {
			fail("%s: at least one calendar is required", where)
		}
		switch a.Type {
		case "caldav":
			if a.Endpoint == "" || a.UsernameEnv == "" || a.PasswordEnv == "" {
				fail("%s: endpoint, usernameEnv, passwordEnv are required for caldav", where)
			}
			if a.ClientIDEnv != "" || a.ClientSecretEnv != "" || a.RefreshTokenEnv != "" {
				fail("%s: google-only fields set on caldav account", where)
			}
		case "google":
			if a.ClientIDEnv == "" || a.ClientSecretEnv == "" || a.RefreshTokenEnv == "" {
				fail("%s: clientIDEnv, clientSecretEnv, refreshTokenEnv are required for google", where)
			}
			if a.Endpoint != "" || a.UsernameEnv != "" || a.PasswordEnv != "" {
				fail("%s: caldav-only fields set on google account", where)
			}
			if len(a.Identities) > 0 {
				fail("%s: identities is a caldav-only field (Google marks the owner's own attendee entry)", where)
			}
		default:
			fail("%s: type must be caldav or google, got %q", where, a.Type)
		}
		seen := map[string]bool{}
		for j, cal := range a.Calendars {
			cwhere := fmt.Sprintf("%s calendars[%d] (%s)", where, j, cal.Name)
			if cal.Name == "" {
				fail("%s: name is required", cwhere)
			}
			if seen[cal.Name] {
				fail("%s: duplicate calendar name", cwhere)
			}
			seen[cal.Name] = true
			switch a.Type {
			case "caldav":
				if cal.Path == "" {
					fail("%s: path is required for caldav calendars", cwhere)
				}
				if cal.ID != "" {
					fail("%s: id is a google-only field", cwhere)
				}
			case "google":
				if cal.ID == "" {
					fail("%s: id is required for google calendars", cwhere)
				}
				if cal.Path != "" {
					fail("%s: path is a caldav-only field", cwhere)
				}
			}
			calendars[a.Name+"/"+cal.Name] = true
		}
	}

	if len(c.Rules) == 0 {
		fail("at least one rule is required")
	}
	ruleIDs := map[string]bool{}
	for i := range c.Rules {
		r := &c.Rules[i]
		where := fmt.Sprintf("rules[%d] (%s)", i, r.ID)
		if r.ID == "" {
			fail("%s: id is required", where)
		}
		if ruleIDs[r.ID] {
			fail("%s: duplicate rule id (marker collision)", where)
		}
		ruleIDs[r.ID] = true
		if !calendars[r.From] {
			fail("%s: from %q is not a configured calendar", where, r.From)
		}
		if len(r.To) == 0 {
			fail("%s: at least one destination is required", where)
		}
		for _, to := range r.To {
			if !calendars[to] {
				fail("%s: to %q is not a configured calendar", where, to)
			}
			if to == r.From {
				fail("%s: destination equals source %q", where, to)
			}
		}
		if tr := r.Transform; tr != nil {
			if tr.Visibility != nil {
				if _, ok := visibilityNames[*tr.Visibility]; !ok {
					fail("%s: transform.visibility %q (want public, private or confidential)", where, *tr.Visibility)
				}
			}
			for _, name := range tr.TransparentForRSVP {
				if _, ok := rsvpNames[name]; !ok {
					fail("%s: transform.transparentForRSVP %q (want needsAction, accepted, declined or tentative)", where, name)
				}
			}
			if tr.Transparent != nil && len(tr.TransparentForRSVP) > 0 {
				fail("%s: transform.transparent and transform.transparentForRSVP both set; one forces opacity for every event, the other derives it from the owner's response", where)
			}
		}
		if f := r.Filter; f != nil {
			for _, wd := range f.Weekdays {
				if _, ok := weekdayNames[wd]; !ok {
					fail("%s: weekday %q (want mon..sun)", where, wd)
				}
			}
			if f.Window != "" && !windowRe.MatchString(f.Window) {
				fail("%s: window %q (want HH:MM-HH:MM, not crossing midnight)", where, f.Window)
			}
			if f.Window != "" {
				if s, e, err := parseWindow(f.Window); err == nil && e <= s {
					fail("%s: window %q: end must be after start (midnight-crossing windows are not supported)", where, f.Window)
				}
			}
			if (f.Window != "" || len(f.Weekdays) > 0) && f.Timezone == "" {
				fail("%s: timezone is required when window or weekdays are set", where)
			}
			if f.Timezone != "" {
				if _, err := time.LoadLocation(f.Timezone); err != nil {
					fail("%s: timezone %q: %v", where, f.Timezone, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// parseWindow returns start/end as minutes since local midnight.
func parseWindow(w string) (startMin, endMin int, err error) {
	m := windowRe.FindStringSubmatch(w)
	if m == nil {
		return 0, 0, fmt.Errorf("window %q: want HH:MM-HH:MM", w)
	}
	toMin := func(h, min string) int {
		return int(h[0]-'0')*600 + int(h[1]-'0')*60 + int(min[0]-'0')*10 + int(min[1]-'0')
	}
	return toMin(m[1], m[2]), toMin(m[3], m[4]), nil
}
