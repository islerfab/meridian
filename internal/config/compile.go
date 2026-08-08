package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/google/cel-go/cel"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/adapter/caldav"
	"github.com/islerfab/meridian/internal/adapter/google"
	"github.com/islerfab/meridian/internal/model"
	"github.com/islerfab/meridian/internal/notify"
	"github.com/islerfab/meridian/internal/sync"
)

// TemplateContext is the data available to transform templates
// (e.g. "[{{ .SourceCalendar }}] {{ .Title }}"). Public API — extend, never
// rename.
type TemplateContext struct {
	Title           string
	Description     string
	Location        string
	SourceCalendar  string
	Organizer       string
	Status          string
	Start           time.Time
	End             time.Time
	AllDay          bool
	DurationMinutes int
}

// CompileRules turns validated rule configs into engine rules. All CEL and
// template compilation happens here — after this returns nil error, rules
// cannot fail to parse at runtime.
func CompileRules(cfg *Config) ([]sync.Rule, error) {
	env, err := newCELEnv()
	if err != nil {
		return nil, fmt.Errorf("cel: %w", err)
	}
	rules := make([]sync.Rule, 0, len(cfg.Rules))
	for _, rc := range cfg.Rules {
		filter, err := compileFilter(env, rc.Filter)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rc.ID, err)
		}
		transform, err := compileTransform(rc)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rc.ID, err)
		}
		rules = append(rules, sync.Rule{
			ID:        rc.ID,
			From:      rc.From,
			To:        rc.To,
			Filter:    filter,
			Transform: transform,
		})
	}
	return rules, nil
}

// --- filter ----------------------------------------------------------------

type compiledFilter struct {
	weekdays        map[time.Weekday]bool // nil = all days
	winStart        int                   // minutes since local midnight; -1 = no window
	winEnd          int
	loc             *time.Location // nil = no weekday/window constraints
	skipTransparent bool
	skipAllDay      bool
	when            cel.Program // nil = no expression
}

func compileFilter(env *cel.Env, fc *FilterConfig) (func(model.Event) bool, error) {
	if fc == nil {
		return func(model.Event) bool { return true }, nil
	}
	f := compiledFilter{winStart: -1, winEnd: -1,
		skipTransparent: fc.SkipTransparent, skipAllDay: fc.SkipAllDay}
	if len(fc.Weekdays) > 0 {
		f.weekdays = map[time.Weekday]bool{}
		for _, wd := range fc.Weekdays {
			f.weekdays[weekdayNames[wd]] = true
		}
	}
	if fc.Window != "" {
		s, e, err := parseWindow(fc.Window)
		if err != nil {
			return nil, err
		}
		f.winStart, f.winEnd = s, e
	}
	if fc.Timezone != "" {
		loc, err := time.LoadLocation(fc.Timezone)
		if err != nil {
			return nil, fmt.Errorf("timezone: %w", err)
		}
		if f.weekdays != nil || f.winStart >= 0 {
			f.loc = loc
		}
	}
	if fc.When != "" {
		prg, err := compileWhen(env, fc.When)
		if err != nil {
			return nil, err
		}
		f.when = prg
	}
	return f.match, nil
}

func (f compiledFilter) match(ev model.Event) bool {
	if f.skipAllDay && ev.AllDay {
		return false
	}
	if f.skipTransparent && ev.Transparent {
		return false
	}
	if !f.overlapsRegion(ev) {
		return false
	}
	if f.when != nil {
		out, _, err := f.when.Eval(map[string]any{"event": celEventFromModel(ev)})
		if err != nil {
			slog.Warn("filter.when evaluation failed, event not matched", "err", err)
			return false
		}
		match, ok := out.Value().(bool)
		return ok && match
	}
	return true
}

// maxRegionDays bounds the day walk (the sync window is ~91 days; anything
// longer cannot be a real fetched event).
const maxRegionDays = 400

// overlapsRegion implements the weekly-region semantics (decided
// 2026-08-08): weekdays+window+timezone define a recurring region; a timed
// event matches when any part of it overlaps the region; an all-day event
// is governed by its dates' weekdays only and is never time-windowed.
func (f compiledFilter) overlapsRegion(ev model.Event) bool {
	if f.loc == nil {
		return true
	}
	matchDay := func(wd time.Weekday) bool { return f.weekdays == nil || f.weekdays[wd] }

	if ev.AllDay {
		// All-day values are calendar dates (midnight-UTC instants): the
		// weekday IS the date's weekday, independent of the rule timezone.
		days := 0
		for d := ev.Start.UTC(); d.Before(ev.End.UTC()) && days < maxRegionDays; d = d.AddDate(0, 0, 1) {
			if matchDay(d.Weekday()) {
				return true
			}
			days++
		}
		return false
	}

	start, end := ev.Start.In(f.loc), ev.End.In(f.loc)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, f.loc)
	for days := 0; day.Before(end) && days < maxRegionDays; days++ {
		next := day.AddDate(0, 0, 1)
		if matchDay(day.Weekday()) {
			if f.winStart < 0 {
				return true // weekday-only region: touching the day suffices
			}
			segStart := day.Add(time.Duration(f.winStart) * time.Minute)
			segEnd := day.Add(time.Duration(f.winEnd) * time.Minute)
			if start.Before(segEnd) && (end.After(segStart) || (start.Equal(end) && !start.Before(segStart))) {
				return true
			}
		}
		day = next
	}
	return false
}

// --- transform -------------------------------------------------------------

type compiledTransform struct {
	title, description, location *template.Template // nil = copy source
	dropTitle, dropDesc, dropLoc bool
	transparent                  *bool
	reminders                    []int
}

func compileTransform(rc RuleConfig) (func(model.Event) model.ShadowContent, error) {
	var ct compiledTransform
	if tc := rc.Transform; tc != nil {
		var err error
		if ct.title, ct.dropTitle, err = compileField("title", tc.Title); err != nil {
			return nil, err
		}
		if ct.description, ct.dropDesc, err = compileField("description", tc.Description); err != nil {
			return nil, err
		}
		if ct.location, ct.dropLoc, err = compileField("location", tc.Location); err != nil {
			return nil, err
		}
		ct.transparent = tc.Transparent
		ct.reminders = tc.Reminders
	}
	return ct.apply, nil
}

// compileField parses one string transform: nil = copy, "drop" = empty,
// anything else = Go template (a plain literal is a trivial template). A
// test execution against a zero context catches bad field references at
// load, not mid-sync.
func compileField(name string, value *string) (*template.Template, bool, error) {
	if value == nil {
		return nil, false, nil
	}
	if *value == Drop {
		return nil, true, nil
	}
	tmpl, err := template.New(name).Parse(*value)
	if err != nil {
		return nil, false, fmt.Errorf("transform.%s: %w", name, err)
	}
	if err := tmpl.Execute(&strings.Builder{}, TemplateContext{}); err != nil {
		return nil, false, fmt.Errorf("transform.%s: %w", name, err)
	}
	return tmpl, false, nil
}

func (ct compiledTransform) apply(ev model.Event) model.ShadowContent {
	tctx := TemplateContext{
		Title:           ev.Title,
		Description:     ev.Description,
		Location:        ev.Location,
		SourceCalendar:  ev.Ref.Calendar,
		Organizer:       ev.Organizer,
		Status:          string(ev.Status),
		Start:           ev.Start,
		End:             ev.End,
		AllDay:          ev.AllDay,
		DurationMinutes: ev.DurationMinutes(),
	}
	content := model.ShadowContent{
		Start:       ev.Start,
		End:         ev.End,
		AllDay:      ev.AllDay,
		Transparent: ev.Transparent,
		Title:       stringField(ev.Title, ct.title, ct.dropTitle, tctx),
		Description: stringField(ev.Description, ct.description, ct.dropDesc, tctx),
		Location:    stringField(ev.Location, ct.location, ct.dropLoc, tctx),
		Reminders:   ct.reminders,
	}
	if ct.transparent != nil {
		content.Transparent = *ct.transparent
	}
	return content
}

func stringField(source string, tmpl *template.Template, drop bool, tctx TemplateContext) string {
	switch {
	case drop:
		return ""
	case tmpl == nil:
		return source
	default:
		var b strings.Builder
		if err := tmpl.Execute(&b, tctx); err != nil {
			// Parse + zero-context execution passed at load; a runtime
			// failure here is exotic — fail safe to the source value.
			slog.Warn("transform template failed, copying source value", "err", err)
			return source
		}
		return b.String()
	}
}

// --- adapters & notifier ---------------------------------------------------

// BuildAdapters constructs one adapter per configured calendar, keyed by
// "<account>/<calendar>". Secret env vars are resolved here; unset vars are
// startup errors naming the variable.
func BuildAdapters(ctx context.Context, cfg *Config, log *slog.Logger) (map[string]adapter.CalendarAdapter, error) {
	adapters := map[string]adapter.CalendarAdapter{}
	for _, a := range cfg.Accounts {
		for _, cal := range a.Calendars {
			key := a.Name + "/" + cal.Name
			switch a.Type {
			case "caldav":
				user, err := requireEnv(a.UsernameEnv, a.Name)
				if err != nil {
					return nil, err
				}
				pass, err := requireEnv(a.PasswordEnv, a.Name)
				if err != nil {
					return nil, err
				}
				ad, err := caldav.New(caldav.Config{
					Endpoint:     a.Endpoint,
					Username:     user,
					Password:     pass,
					CalendarPath: cal.Path,
					CalendarID:   key,
					InstanceID:   cfg.Instance,
					Expansion:    a.Expansion,
				}, log)
				if err != nil {
					return nil, err
				}
				adapters[key] = ad
			case "google":
				id, err := requireEnv(a.ClientIDEnv, a.Name)
				if err != nil {
					return nil, err
				}
				secret, err := requireEnv(a.ClientSecretEnv, a.Name)
				if err != nil {
					return nil, err
				}
				token, err := requireEnv(a.RefreshTokenEnv, a.Name)
				if err != nil {
					return nil, err
				}
				ad, err := google.New(ctx, google.Config{
					ClientID:         id,
					ClientSecret:     secret,
					RefreshToken:     token,
					CalendarID:       key,
					GoogleCalendarID: cal.ID,
					InstanceID:       cfg.Instance,
				}, log)
				if err != nil {
					return nil, err
				}
				adapters[key] = ad
			}
		}
	}
	return adapters, nil
}

// BuildNotifier resolves the notification channel (Nop when unconfigured).
func BuildNotifier(cfg *Config) (notify.Notifier, error) {
	envName := cfg.Notifications.DiscordWebhookURLEnv
	if envName == "" {
		return notify.Nop{}, nil
	}
	url := os.Getenv(envName)
	if url == "" {
		return nil, fmt.Errorf("notifications: env var %s is empty", envName)
	}
	return &notify.Discord{WebhookURL: url}, nil
}

func requireEnv(name, account string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("account %s: env reference field is empty", account)
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("account %s: env var %s is empty", account, name)
	}
	return v, nil
}
