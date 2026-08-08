package model

import (
	"fmt"
	"strconv"
	"strings"
)

// MarkerVersion is the current marker format version, stored in
// meridian.v / X-MERIDIAN-V.
const MarkerVersion = 1

// Property keys for the marker on each protocol (DESIGN.md Decision 2).
// Google: extendedProperties.private keys. CalDAV: X- properties on the
// shadow VEVENT.
const (
	GoogleKeySrc  = "meridian.src"
	GoogleKeyRule = "meridian.rule"
	GoogleKeyHash = "meridian.hash"
	GoogleKeyV    = "meridian.v"

	CalDAVPropSrc  = "X-MERIDIAN-SRC"
	CalDAVPropRule = "X-MERIDIAN-RULE"
	CalDAVPropHash = "X-MERIDIAN-HASH"
	CalDAVPropV    = "X-MERIDIAN-V"
)

// Marker is the ownership marker every shadow event carries. It is the only
// cross-cycle state meridian has: Src keys change detection and orphan GC,
// Rule scopes shadows to the rule that created them, Hash is the
// content hash of the post-transform desired content.
type Marker struct {
	Src  EventRef
	Rule string
	Hash string
	V    int
}

// NewMarker builds a current-version marker for desired content.
func NewMarker(src EventRef, rule string, content ShadowContent) Marker {
	return Marker{Src: src, Rule: rule, Hash: ContentHash(content), V: MarkerVersion}
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
// google selects the Google extendedProperties.private key set; otherwise
// the CalDAV X-MERIDIAN-* set. Adapters translate the map into their SDK's
// representation; nothing protocol-specific lives beyond the key names.
func (m Marker) Properties(google bool) map[string]string {
	src, rule, hash, v := keysFor(google)
	return map[string]string{
		src:  m.Src.String(),
		rule: m.Rule,
		hash: m.Hash,
		v:    strconv.Itoa(m.V),
	}
}

// ParseMarker decodes a marker from protocol properties (the inverse of
// Properties). found is false when none of the marker keys are present —
// the event is foreign and must never be touched. A present-but-malformed
// marker returns an error; callers surface it loudly rather than silently
// treating an owned-looking event as foreign.
func ParseMarker(props map[string]string, google bool) (m Marker, found bool, err error) {
	srcKey, ruleKey, hashKey, vKey := keysFor(google)
	srcRaw, srcOK := props[srcKey]
	rule, ruleOK := props[ruleKey]
	hash, hashOK := props[hashKey]
	vRaw, vOK := props[vKey]
	if !srcOK && !ruleOK && !hashOK && !vOK {
		return Marker{}, false, nil
	}
	if !srcOK || !ruleOK || !hashOK || !vOK {
		return Marker{}, true, fmt.Errorf("marker incomplete: have src=%t rule=%t hash=%t v=%t", srcOK, ruleOK, hashOK, vOK)
	}
	v, err := strconv.Atoi(vRaw)
	if err != nil {
		return Marker{}, true, fmt.Errorf("marker version %q: %w", vRaw, err)
	}
	if v != MarkerVersion {
		return Marker{}, true, fmt.Errorf("marker version %d not supported (current %d)", v, MarkerVersion)
	}
	src, err := ParseEventRef(srcRaw)
	if err != nil {
		return Marker{}, true, fmt.Errorf("marker: %w", err)
	}
	if rule == "" || hash == "" {
		return Marker{}, true, fmt.Errorf("marker: empty rule or hash")
	}
	return Marker{Src: src, Rule: rule, Hash: hash, V: v}, true, nil
}

func keysFor(google bool) (src, rule, hash, v string) {
	if google {
		return GoogleKeySrc, GoogleKeyRule, GoogleKeyHash, GoogleKeyV
	}
	return CalDAVPropSrc, CalDAVPropRule, CalDAVPropHash, CalDAVPropV
}
