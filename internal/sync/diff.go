package sync

import (
	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

// OpKind is a reconciliation operation type.
type OpKind string

const (
	OpCreate OpKind = "create"
	OpUpdate OpKind = "update"
	OpDelete OpKind = "delete"
)

// OpReason is why an op was planned. A closed set compared across files
// (engine.go's mass-delete guard classifies on it) — a named type catches
// a typo or a new reason at compile time instead of silently undercounting
// a guard.
type OpReason string

const (
	ReasonNew            OpReason = "new"
	ReasonContentChanged OpReason = "content-changed"
	ReasonOrphan         OpReason = "orphan"
	ReasonDuplicate      OpReason = "duplicate"
	ReasonDriftRepair    OpReason = "drift-repair"
)

// Op is one planned write against a destination calendar.
type Op struct {
	Kind   OpKind
	Dest   string
	Shadow model.Shadow // full desired shadow for create/update; Ref (+ old marker) for delete
	Reason OpReason
}

// diff computes the op set that converges one destination onto the desired
// set for one rule. Pure function — all guard semantics that shape the diff
// live here:
//
//   - change detection: desired content hash vs the hash STORED IN THE
//     MARKER; provider-returned field values are never compared
//   - windowed orphan GC: deletes only shadows overlapping the window
//     (guard 4) — shadows outside it are never touched
//   - duplicates: one shadow per (rule, src); a hash-matching duplicate is
//     kept over the first-listed one, extras deleted
//   - shadows of OTHER rules are invisible here (caller filters by rule ID);
//     shadows of unknown/removed rules are deliberately not swept
func diff(dest, instance, rule string, desired map[model.EventRef]model.ShadowContent, actual []model.Shadow, window adapter.Window) []Op {
	var ops []Op

	// Group the rule's shadows by source ref.
	bysrc := make(map[model.EventRef][]model.Shadow)
	for _, s := range actual {
		bysrc[s.Marker.Src] = append(bysrc[s.Marker.Src], s)
	}

	for src, content := range desired {
		wantHash := model.ContentHash(content)
		existing, ok := bysrc[src]
		if !ok {
			ops = append(ops, Op{
				Kind: OpCreate,
				Dest: dest,
				Shadow: model.Shadow{
					Content: content,
					Marker:  model.NewMarker(instance, src, rule, content),
				},
				Reason: ReasonNew,
			})
			continue
		}
		// Dedupe: prefer a keeper that already matches the desired hash.
		keeper := existing[0]
		for _, s := range existing {
			if s.Marker.Hash == wantHash {
				keeper = s
				break
			}
		}
		for _, s := range existing {
			if s.Ref != keeper.Ref {
				ops = append(ops, Op{Kind: OpDelete, Dest: dest, Shadow: s, Reason: ReasonDuplicate})
			}
		}
		if keeper.Marker.Hash != wantHash {
			ops = append(ops, Op{
				Kind: OpUpdate,
				Dest: dest,
				Shadow: model.Shadow{
					Ref:     keeper.Ref,
					Content: content,
					Marker:  model.NewMarker(instance, src, rule, content),
				},
				Reason: ReasonContentChanged,
			})
		}
	}

	// Orphan GC, windowed (guard 4).
	for src, existing := range bysrc {
		if _, wanted := desired[src]; wanted {
			continue
		}
		for _, s := range existing {
			if !window.Overlaps(s.Content.Start, s.Content.End) {
				continue // never GC outside the window
			}
			ops = append(ops, Op{Kind: OpDelete, Dest: dest, Shadow: s, Reason: ReasonOrphan})
		}
	}
	return ops
}
