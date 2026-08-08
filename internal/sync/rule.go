package sync

import "github.com/islerfab/meridian/internal/model"

// Rule is one compiled reconciliation rule as the engine consumes it. The
// config layer (rules.yaml + CEL + templates, mer-gyw) compiles into this
// shape; the engine never sees YAML, expressions, or templates.
type Rule struct {
	// ID is the stable rule identifier stored in shadow markers. Changing
	// an ID orphans the rule's shadows (Decision 2).
	ID string
	// From is the logical source calendar ID (adapter map key).
	From string
	// To lists logical destination calendar IDs.
	To []string
	// Filter reports whether a source event is selected. Never called for
	// meridian-owned events (the zombie guard runs first).
	Filter func(model.Event) bool
	// Transform computes the post-transform desired shadow content.
	Transform func(model.Event) model.ShadowContent
}
