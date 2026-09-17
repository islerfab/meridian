package config

import (
	"testing"
	"time"

	"github.com/islerfab/meridian/internal/model"
)

// The config layer is the only part of meridian that consumes input a human
// wrote by hand, and every failure in it has to be a startup error rather than
// a panic: a pod that crash-loops on a typo is worse than one that refuses to
// start and says why.

func FuzzLoad(f *testing.F) {
	f.Add(validYAML)
	f.Add("")
	f.Add("instance: x\n")
	f.Add("rules:\n  - id: a\n    from: p/m\n    to: [w/p]\n")
	f.Add("accounts:\n  - name: a\n    type: caldav\n")
	f.Add("interval: -1s\n")
	f.Add("rules:\n  - filter:\n      window: \"99:99-00:00\"\n")

	f.Fuzz(func(t *testing.T, content string) {
		cfg, err := loadString(t, content)
		if err != nil {
			return
		}
		if cfg == nil {
			t.Fatal("Load returned a nil config and a nil error")
		}
		// Anything that loads must survive compilation. Load documents itself
		// as validating windows, references, CEL and templates, so a config it
		// accepted must not blow up the stage that consumes those.
		if _, err := CompileRules(cfg); err != nil {
			return
		}
		if cfg.IntervalDuration <= 0 {
			t.Fatalf("Load accepted a config with a non-positive interval: %v", cfg.IntervalDuration)
		}
	})
}

// FuzzCompileWhen covers filter.when, the one place a user supplies an
// expression language rather than a typed field.
func FuzzCompileWhen(f *testing.F) {
	f.Add(`event.title == "x"`)
	f.Add(`!event.title.startsWith("[private]")`)
	f.Add(`event.durationMinutes > 30 && !event.allDay`)
	f.Add(`event.attendees.size() > 0`)
	f.Add("")
	f.Add("event")
	f.Add("event.nope")
	f.Add("1 + ")

	env, err := newCELEnv()
	if err != nil {
		f.Fatalf("newCELEnv: %v", err)
	}

	ev := celEventFromModel(model.Event{
		Title: "x",
		Start: time.Unix(0, 0).UTC(),
		End:   time.Unix(3600, 0).UTC(),
	})

	f.Fuzz(func(t *testing.T, expr string) {
		prg, err := compileWhen(env, expr)
		if err != nil {
			return
		}
		// compileWhen already rejects anything not typed bool, so a program it
		// returns must both evaluate and yield a bool.
		out, _, err := prg.Eval(map[string]any{"event": ev})
		if err != nil {
			return // runtime errors are the caller's to handle, panics are not
		}
		if _, ok := out.Value().(bool); !ok {
			t.Fatalf("when %q type-checked as bool but evaluated to %T", expr, out.Value())
		}
	})
}
