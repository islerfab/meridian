package model

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

// hashPrefix versions the canonical serialization itself. Bumping it (or
// changing anything below) changes every hash and therefore rewrites every
// shadow on the next cycle — the format is a persisted contract, treat it
// like a wire format.
const hashPrefix = "meridian-hash-v2" // v2: added color field

// ContentHash computes the change-detection hash stored in the marker:
// SHA-256 (full hex) over a canonical, versioned serialization of the
// post-transform desired content. Provider-returned field values are never
// compared against this — only the hash stored in the marker is.
func ContentHash(c ShadowContent) string {
	sum := sha256.Sum256([]byte(canonicalContent(c)))
	return hex.EncodeToString(sum[:])
}

// canonicalContent renders the fixed-order key=value line format. Rules:
// one field per line in the order below, string values escaped so embedded
// newlines cannot forge field boundaries, times normalized to UTC RFC 3339,
// reminders sorted ascending and comma-joined.
func canonicalContent(c ShadowContent) string {
	reminders := make([]string, len(c.Reminders))
	sorted := append([]int(nil), c.Reminders...)
	sort.Ints(sorted)
	for i, m := range sorted {
		reminders[i] = strconv.Itoa(m)
	}

	var b strings.Builder
	b.WriteString(hashPrefix)
	b.WriteByte('\n')
	writeField(&b, "title", escapeHashValue(c.Title))
	writeField(&b, "description", escapeHashValue(c.Description))
	writeField(&b, "location", escapeHashValue(c.Location))
	writeField(&b, "start", c.Start.UTC().Format(time.RFC3339))
	writeField(&b, "end", c.End.UTC().Format(time.RFC3339))
	writeField(&b, "allDay", strconv.FormatBool(c.AllDay))
	writeField(&b, "transparent", strconv.FormatBool(c.Transparent))
	writeField(&b, "reminders", strings.Join(reminders, ","))
	writeField(&b, "color", escapeHashValue(c.Color))
	return b.String()
}

func writeField(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(value)
	b.WriteByte('\n')
}

var hashValueEscaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r")

func escapeHashValue(s string) string {
	return hashValueEscaper.Replace(s)
}
