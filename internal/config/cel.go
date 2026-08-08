package config

import (
	"fmt"
	"reflect"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"

	"github.com/islerfab/meridian/internal/model"
)

// CELEvent is the documented, versioned event schema exposed to filter.when
// expressions (DESIGN.md Decision 4). Field names come from the cel tags;
// this is a public API — extend, never rename.
type CELEvent struct {
	Title           string    `cel:"title"`
	Description     string    `cel:"description"`
	Location        string    `cel:"location"`
	Start           time.Time `cel:"start"`
	End             time.Time `cel:"end"`
	DurationMinutes int64     `cel:"durationMinutes"`
	AllDay          bool      `cel:"allDay"`
	Transparent     bool      `cel:"transparent"`
	Status          string    `cel:"status"`
	Organizer       string    `cel:"organizer"`
	Attendees       []string  `cel:"attendees"`
}

func celEventFromModel(ev model.Event) CELEvent {
	return CELEvent{
		Title:           ev.Title,
		Description:     ev.Description,
		Location:        ev.Location,
		Start:           ev.Start,
		End:             ev.End,
		DurationMinutes: int64(ev.DurationMinutes()),
		AllDay:          ev.AllDay,
		Transparent:     ev.Transparent,
		Status:          string(ev.Status),
		Organizer:       ev.Organizer,
		Attendees:       ev.Attendees,
	}
}

// newCELEnv builds the compile environment: the `event` variable typed
// against CELEvent so bad field names and type errors fail at config load.
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		ext.NativeTypes(ext.ParseStructTags(true), reflect.TypeOf(CELEvent{})),
		cel.Variable("event", cel.ObjectType("config.CELEvent")),
	)
}

// compileWhen compiles and type-checks a filter.when expression to a bool
// program.
func compileWhen(env *cel.Env, expr string) (cel.Program, error) {
	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return nil, fmt.Errorf("when %q: %w", expr, iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("when %q: must evaluate to bool, got %s", expr, ast.OutputType())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("when %q: %w", expr, err)
	}
	return prg, nil
}
