package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
instance: test-instance
interval: 2m
notifications:
  webhookURLEnv: MERIDIAN_NOTIFY_WEBHOOK
guards:
  massDeleteFraction: 0.7
accounts:
  - name: private
    type: caldav
    endpoint: https://sync.example.com
    usernameEnv: CALDAV_USER
    passwordEnv: CALDAV_PASS
    calendars:
      - name: main
        path: /calendars/u/x/
  - name: work
    type: google
    clientIDEnv: G_ID
    clientSecretEnv: G_SECRET
    refreshTokenEnv: G_TOKEN
    calendars:
      - name: primary
        id: abc@group.calendar.google.com
rules:
  - id: busy
    from: private/main
    to: [work/primary]
    filter:
      weekdays: [mon, tue, wed, thu, fri]
      window: "08:00-18:00"
      timezone: Europe/Zurich
      skipTransparent: true
      when: '!event.title.startsWith("[private]")'
    transform:
      title: "Busy"
      description: drop
      reminders: [15]
      visibility: private
`

func loadString(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadValid(t *testing.T) {
	cfg, err := loadString(t, validYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance != "test-instance" || cfg.IntervalDuration != 2*time.Minute {
		t.Errorf("cfg = %+v", cfg)
	}
	if *cfg.Guards.MassDeleteFraction != 0.7 {
		t.Errorf("massDeleteFraction = %v", *cfg.Guards.MassDeleteFraction)
	}
	if _, err := CompileRules(cfg); err != nil {
		t.Errorf("CompileRules: %v", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	minimal := `
instance: i
accounts:
  - name: a
    type: caldav
    endpoint: https://x
    usernameEnv: U
    passwordEnv: P
    calendars: [{name: c, path: /p/}]
  - name: b
    type: google
    clientIDEnv: I
    clientSecretEnv: S
    refreshTokenEnv: T
    calendars: [{name: c, id: x@y}]
rules:
  - id: r
    from: a/c
    to: [b/c]
`
	cfg, err := loadString(t, minimal)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntervalDuration != 5*time.Minute {
		t.Errorf("default interval = %v", cfg.IntervalDuration)
	}
	if *cfg.Guards.MassDeleteFraction != 0.5 {
		t.Errorf("default massDeleteFraction = %v", *cfg.Guards.MassDeleteFraction)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]struct{ mutate, wantErr string }{
		"unknown field":      {"instance: i\nsurprise: true", "surprise"},
		"missing instance":   {strings.Replace(validYAML, "instance: test-instance", "instance: \"\"", 1), "instance is required"},
		"bad interval":       {strings.Replace(validYAML, "interval: 2m", "interval: fast", 1), "interval"},
		"bad fraction":       {strings.Replace(validYAML, "massDeleteFraction: 0.7", "massDeleteFraction: 1.5", 1), "massDeleteFraction"},
		"bad visibility":     {strings.Replace(validYAML, "visibility: private", "visibility: secret", 1), "transform.visibility"},
		"bad weekday":        {strings.Replace(validYAML, "weekdays: [mon, tue, wed, thu, fri]", "weekdays: [monday]", 1), "weekday"},
		"bad window":         {strings.Replace(validYAML, `window: "08:00-18:00"`, `window: "8-18"`, 1), "window"},
		"inverted window":    {strings.Replace(validYAML, `window: "08:00-18:00"`, `window: "18:00-08:00"`, 1), "midnight"},
		"missing timezone":   {strings.Replace(validYAML, "timezone: Europe/Zurich", "", 1), "timezone is required"},
		"bad timezone":       {strings.Replace(validYAML, "Europe/Zurich", "Mars/Olympus", 1), "timezone"},
		"unknown from":       {strings.Replace(validYAML, "from: private/main", "from: nope/nope", 1), "not a configured calendar"},
		"unknown to":         {strings.Replace(validYAML, "to: [work/primary]", "to: [nope/nope]", 1), "not a configured calendar"},
		"self sync":          {strings.Replace(validYAML, "to: [work/primary]", "to: [private/main]", 1), "destination equals source"},
		"duplicate rule":     {validYAML + "\n  - id: busy\n    from: private/main\n    to: [work/primary]", "duplicate rule id"},
		"caldav with google": {strings.Replace(validYAML, "passwordEnv: CALDAV_PASS", "passwordEnv: CALDAV_PASS\n    clientIDEnv: X", 1), "google-only"},
		"google cal w/o id":  {strings.Replace(validYAML, "id: abc@group.calendar.google.com", "path: /oops/", 1), "id is required"},
		"bad account type":   {strings.Replace(validYAML, "type: caldav", "type: exchange", 1), "type must be"},
		"no rules":           {strings.Split(validYAML, "rules:")[0], "at least one rule"},
	}
	for name, c := range cases {
		_, err := loadString(t, c.mutate)
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %q does not mention %q", name, err, c.wantErr)
		}
	}
}

func TestCompileRejectsBadCELAndTemplates(t *testing.T) {
	cases := map[string]struct{ old, new, wantErr string }{
		"cel syntax":     {`when: '!event.title.startsWith("[private]")'`, `when: 'event.title ==='`, "when"},
		"cel bad field":  {`when: '!event.title.startsWith("[private]")'`, `when: 'event.nonexistent == "x"'`, "nonexistent"},
		"cel non-bool":   {`when: '!event.title.startsWith("[private]")'`, `when: 'event.title'`, "bool"},
		"template parse": {`title: "Busy"`, `title: "{{ .Title"`, "title"},
		"template field": {`title: "Busy"`, `title: "{{ .Nonexistent }}"`, "title"},
	}
	for name, c := range cases {
		cfg, err := loadString(t, strings.Replace(validYAML, c.old, c.new, 1))
		if err != nil {
			t.Fatalf("%s: load: %v", name, err)
		}
		_, err = CompileRules(cfg)
		if err == nil {
			t.Errorf("%s: expected compile error", name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error %q does not mention %q", name, err, c.wantErr)
		}
	}
}

func TestLoadRejectsRSVPConfig(t *testing.T) {
	base := `
instance: i
accounts:
  - name: a
    type: caldav
    endpoint: https://x
    usernameEnv: U
    passwordEnv: P
    identities: [me@example.com]
    calendars: [{name: c, path: /p/}]
  - name: b
    type: google
    clientIDEnv: I
    clientSecretEnv: S
    refreshTokenEnv: T
    calendars: [{name: c, id: x@y}]
rules:
  - id: r
    from: a/c
    to: [b/c]
    transform:
`
	cases := []struct {
		name    string
		tail    string
		wantErr string
	}{
		{"unknown rsvp value", "      transparentForRSVP: [maybe]\n", "transparentForRSVP"},
		{"transparent and transparentForRSVP together",
			"      transparent: false\n      transparentForRSVP: [needsAction]\n", "both set"},
		{"valid list", "      transparentForRSVP: [needsAction, tentative]\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadString(t, base+c.tail)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("want no error, got %v", err)
			case c.wantErr != "" && err == nil:
				t.Fatal("want an error, got nil")
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Errorf("error %v should mention %q", err, c.wantErr)
			}
		})
	}
}

func TestLoadRejectsIdentitiesOnGoogleAccount(t *testing.T) {
	cfg := `
instance: i
accounts:
  - name: b
    type: google
    clientIDEnv: I
    clientSecretEnv: S
    refreshTokenEnv: T
    identities: [me@example.com]
    calendars: [{name: c, id: x@y}]
  - name: a
    type: caldav
    endpoint: https://x
    usernameEnv: U
    passwordEnv: P
    calendars: [{name: c, path: /p/}]
rules:
  - id: r
    from: b/c
    to: [a/c]
`
	_, err := loadString(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "identities is a caldav-only field") {
		t.Fatalf("want a caldav-only rejection, got %v", err)
	}
}
