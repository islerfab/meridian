package model

import (
	"fmt"
	"strconv"
	"strings"
)

// MarkerVersion is the current marker format version, stored in
// meridian.v / X-MERIDIAN-V. ParseMarker accepts this version and every
// older one it still knows how to decode (mer-dtd, DESIGN.md Decision 2:
// backward-compatible reads, forward-only writes) — a version bump alone
// must never make existing shadows invisible to the engine. NewMarker
// always stamps the current version; a version whose old shape can no
// longer be decoded into the current Marker at all is the one case that
// needs a real migration or a wipe, not silently handled here.
const MarkerVersion = 2

// Protocol selects which adapter's marker key scheme to use. A closed set
// of exactly the two protocols meridian speaks (DESIGN.md Decision 5) — a
// named type reads at call sites (ParseMarker(props, ProtocolGoogle)),
// where a bare bool would not.
type Protocol int

const (
	ProtocolGoogle Protocol = iota
	ProtocolCalDAV
)

// Property keys for the marker on each protocol (DESIGN.md Decision 2).
// Google: extendedProperties.private keys. CalDAV: X- properties on the
// shadow VEVENT.
const (
	GoogleKeySrc      = "meridian.src"
	GoogleKeyRule     = "meridian.rule"
	GoogleKeyHash     = "meridian.hash"
	GoogleKeyInstance = "meridian.instance"
	GoogleKeyRepair   = "meridian.repair"
	GoogleKeyV        = "meridian.v"

	CalDAVPropSrc      = "X-MERIDIAN-SRC"
	CalDAVPropRule     = "X-MERIDIAN-RULE"
	CalDAVPropHash     = "X-MERIDIAN-HASH"
	CalDAVPropInstance = "X-MERIDIAN-INSTANCE"
	CalDAVPropRepair   = "X-MERIDIAN-REPAIR"
	CalDAVPropV        = "X-MERIDIAN-V"
)

// Marker is the ownership marker every shadow event carries. It is the only
// cross-cycle state meridian has: Src keys change detection and orphan GC,
// Instance+Rule scope shadows to the meridian instance and rule that created
// them (instance+rule is globally unique — this is what makes multiple
// instances feeding one destination safe), Hash is the content hash of the
// post-transform desired content. RepairTries counts bounded drift-repair
// attempts against this exact Hash (mer-t75) — it resets to 0 whenever
// NewMarker is called for genuinely new/changed content, since that always
// produces a new Hash.
type Marker struct {
	Src         EventRef
	Rule        string
	Hash        string
	Instance    string
	RepairTries int
	V           int
}

// NewMarker builds a current-version marker for desired content.
// RepairTries starts at 0: this always represents fresh intended content,
// never a repair attempt (see Engine.reconcileRule, which stamps
// RepairTries itself when building a drift-repair op).
func NewMarker(instance string, src EventRef, rule string, content ShadowContent) Marker {
	return Marker{Src: src, Rule: rule, Hash: ContentHash(content), Instance: instance, V: MarkerVersion}
}

// --- Src composite ---------------------------------------------------------

// String encodes the ref as the readable pipe-joined composite stored in
// meridian.src / X-MERIDIAN-SRC: calendar|uid|recurrenceID, with % and |
// percent-escaped inside components. Format is positional and versioned by
// the marker's V field.
func (r EventRef) String() string {
	return escapeRefPart(r.Calendar) + "|" + escapeRefPart(r.UID) + "|" + escapeRefPart(r.RecurrenceID)
}

// ParseEventRef decodes the pipe-joined composite produced by String.
func ParseEventRef(s string) (EventRef, error) {
	parts := strings.Split(s, "|")
	if len(parts) != 3 {
		return EventRef{}, fmt.Errorf("source ref %q: expected 3 pipe-separated fields, got %d", s, len(parts))
	}
	return EventRef{
		Calendar:     unescapeRefPart(parts[0]),
		UID:          unescapeRefPart(parts[1]),
		RecurrenceID: unescapeRefPart(parts[2]),
	}, nil
}

func escapeRefPart(s string) string {
	s = strings.ReplaceAll(s, "%", "%25") // must run first
	return strings.ReplaceAll(s, "|", "%7C")
}

func unescapeRefPart(s string) string {
	s = strings.ReplaceAll(s, "%7C", "|")
	return strings.ReplaceAll(s, "%25", "%") // must run last
}

// --- protocol codecs -------------------------------------------------------

// Properties returns the marker as protocol property key/value pairs.
// Adapters translate the map into their SDK's representation; nothing
// protocol-specific lives beyond the key names.
func (m Marker) Properties(p Protocol) map[string]string {
	src, rule, hash, instance, repair, v := keysFor(p)
	return map[string]string{
		src:      m.Src.String(),
		rule:     m.Rule,
		hash:     m.Hash,
		instance: m.Instance,
		repair:   strconv.Itoa(m.RepairTries),
		v:        strconv.Itoa(m.V),
	}
}

// ParseMarker decodes a marker from protocol properties (the inverse of
// Properties). found is false when none of the marker keys are present —
// the event is foreign and must never be touched. A present-but-malformed
// marker returns an error; callers surface it loudly rather than silently
// treating an owned-looking event as foreign.
//
// Backward-compatible reads, forward-only writes (mer-dtd, DESIGN.md
// Decision 2): every marker version this binary still knows how to decode
// is handled below via an explicit per-version case — deliberately not a
// generic schema-evolution framework, since format changes should be rare.
// src/rule/hash/instance/v are the version-independent core, present in
// every version so far; version-gated fields (repair, added in v2) are
// validated inside their version's case, with older versions defaulting
// them to their historically-correct value rather than treating absence as
// malformed. Properties/NewMarker always write the current MarkerVersion —
// nothing here ever produces an old-format marker.
func ParseMarker(props map[string]string, p Protocol) (m Marker, found bool, err error) {
	srcKey, ruleKey, hashKey, instanceKey, repairKey, vKey := keysFor(p)
	srcRaw, srcOK := props[srcKey]
	rule, ruleOK := props[ruleKey]
	hash, hashOK := props[hashKey]
	instance, instanceOK := props[instanceKey]
	repairRaw, repairOK := props[repairKey]
	vRaw, vOK := props[vKey]
	if !srcOK && !ruleOK && !hashOK && !instanceOK && !repairOK && !vOK {
		return Marker{}, false, nil
	}
	if !srcOK || !ruleOK || !hashOK || !instanceOK || !vOK {
		return Marker{}, true, fmt.Errorf("marker incomplete: have src=%t rule=%t hash=%t instance=%t v=%t", srcOK, ruleOK, hashOK, instanceOK, vOK)
	}
	v, err := strconv.Atoi(vRaw)
	if err != nil {
		return Marker{}, true, fmt.Errorf("marker version %q: %w", vRaw, err)
	}
	src, err := ParseEventRef(srcRaw)
	if err != nil {
		return Marker{}, true, fmt.Errorf("marker: %w", err)
	}
	if rule == "" || hash == "" || instance == "" {
		return Marker{}, true, fmt.Errorf("marker: empty rule, hash, or instance")
	}

	var repairTries int
	switch v {
	case 1:
		// Pre-repair format: the key never existed, so "never attempted"
		// (0) is the historically correct value, not a placeholder.
	case 2:
		if !repairOK {
			return Marker{}, true, fmt.Errorf("marker incomplete: v2 requires repair, have repair=%t", repairOK)
		}
		repairTries, err = strconv.Atoi(repairRaw)
		if err != nil {
			return Marker{}, true, fmt.Errorf("marker repair count %q: %w", repairRaw, err)
		}
	default:
		return Marker{}, true, fmt.Errorf("marker version %d not supported (recognize 1-%d)", v, MarkerVersion)
	}

	return Marker{Src: src, Rule: rule, Hash: hash, Instance: instance, RepairTries: repairTries, V: v}, true, nil
}

func keysFor(p Protocol) (src, rule, hash, instance, repair, v string) {
	if p == ProtocolGoogle {
		return GoogleKeySrc, GoogleKeyRule, GoogleKeyHash, GoogleKeyInstance, GoogleKeyRepair, GoogleKeyV
	}
	return CalDAVPropSrc, CalDAVPropRule, CalDAVPropHash, CalDAVPropInstance, CalDAVPropRepair, CalDAVPropV
}
