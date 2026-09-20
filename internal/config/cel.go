package config

import (
	"fmt"
	"reflect"
	"time"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/ext"

	"github.com/islerfab/meridian/internal/model"
)

// CELEvent is the documented, versioned event schema exposed to filter.when
// expressions. Field names come from the cel tags; this is a public API —
// extend, never rename.
type CELEvent struct {
	// Title is the event's title.
	Title string `cel:"title"`
	// Description is the event's description.
	Description string `cel:"description"`
	// Location is the event's location.
	Location string `cel:"location"`
	// Start is the event's start instant.
	Start time.Time `cel:"start"`
	// End is the event's end instant.
	End time.Time `cel:"end"`
	// DurationMinutes is End minus Start, in minutes.
	DurationMinutes int64 `cel:"durationMinutes"`
	// AllDay is true for all-day (DATE-valued) events.
	AllDay bool `cel:"allDay"`
	// Transparent is true when the source marks the event as free.
	Transparent bool `cel:"transparent"`
	// Status is the event's status (e.g. confirmed, tentative, cancelled).
	Status string `cel:"status"`
	// Visibility is the event's disclosure class: public, private,
	// confidential, or empty when the source inherits its calendar default.
	Visibility string `cel:"visibility"`
	// Organizer is the event organizer's identifier.
	Organizer string `cel:"organizer"`
	// Attendees lists attendee identifiers.
	Attendees []string `cel:"attendees"`
	// Rsvp is the calendar owner's own response to an invitation:
	// needsAction, accepted, declined or tentative. Empty when the owner
	// has no attendee record, which is every event that isn't an
	// invitation — test for a specific value, never for "not accepted",
	// or the expression also catches ordinary non-meeting events.
	Rsvp string `cel:"rsvp"`
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
		Visibility:      string(ev.Visibility),
		Organizer:       ev.Organizer,
		Attendees:       ev.Attendees,
		Rsvp:            string(ev.RSVP),
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
// program. usesRSVP reports whether the expression reads event.rsvp, which
// callers need to tell a CalDAV source that it cannot answer the question.
func compileWhen(env *cel.Env, expr string) (prg cel.Program, usesRSVP bool, err error) {
	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return nil, false, fmt.Errorf("when %q: %w", expr, iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, false, fmt.Errorf("when %q: must evaluate to bool, got %s", expr, ast.OutputType())
	}
	prg, err = env.Program(ast)
	if err != nil {
		return nil, false, fmt.Errorf("when %q: %w", expr, err)
	}
	return prg, selectsEventField(ast, "rsvp"), nil
}

// selectsEventField reports whether the expression contains an `event.<name>`
// field selection. Walking the checked AST rather than matching on the
// source text so that a string mentioning the field, or a comment, doesn't
// register as a read.
func selectsEventField(a *cel.Ast, name string) bool {
	found := false
	celast.PostOrderVisit(a.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if e.Kind() != celast.SelectKind {
			return
		}
		sel := e.AsSelect()
		if sel.FieldName() != name {
			return
		}
		if op := sel.Operand(); op.Kind() == celast.IdentKind && op.AsIdent() == "event" {
			found = true
		}
	}))
	return found
}
