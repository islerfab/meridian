package main

import (
	"encoding/json"
	"regexp"
	"testing"
)

// configSourceFromTest is configSourceFile as seen from this package's
// directory: gendocs itself always runs from the repo root.
const configSourceFromTest = "../../../" + configSourceFile

func TestJSONTypeFor(t *testing.T) {
	tests := []struct {
		name   string
		goType string
		want   string
	}{
		{"scalar", "string", `{"type":"string"}`},
		{"pointer-accepts-null", "*string", `{"type":["string","null"]}`},
		{"pointer-bool", "*bool", `{"type":["boolean","null"]}`},
		{"pointer-number", "*float64", `{"type":["number","null"]}`},
		{"slice-of-scalar", "[]string", `{"items":{"type":"string"},"type":"array"}`},
		{"slice-of-int", "[]int", `{"items":{"type":"integer"},"type":"array"}`},
		{"nested-struct", "Notifications", `{"$ref":"#/definitions/configNotifications"}`},
		{"pointer-to-struct", "*FilterConfig", `{"$ref":"#/definitions/configFilter"}`},
		{"slice-of-struct", "[]Account", `{"items":{"$ref":"#/definitions/configAccount"},"type":"array"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node, err := jsonTypeFor(tc.goType)
			if err != nil {
				t.Fatalf("jsonTypeFor(%q) errored: %v", tc.goType, err)
			}
			got, err := json.Marshal(node)
			if err != nil {
				t.Fatalf("marshaling %v: %v", node, err)
			}
			if string(got) != tc.want {
				t.Errorf("jsonTypeFor(%q) = %s, want %s", tc.goType, got, tc.want)
			}
		})
	}
}

// An unmapped Go type must stop the build rather than silently emit a
// field with no type at all — that would be a hole in the closed schema,
// which is the one thing it exists to prevent.
func TestJSONTypeForRejectsUnknown(t *testing.T) {
	if _, err := jsonTypeFor("time.Duration"); err == nil {
		t.Fatal("jsonTypeFor(\"time.Duration\") succeeded, want an error")
	}
}

func TestTypedDefault(t *testing.T) {
	tests := []struct {
		name     string
		jsonType any
		raw      string
		want     any
	}{
		{"string-stays-string", "string", "5m", "5m"},
		{"bool", "boolean", "false", false},
		{"number", "number", "0.5", 0.5},
		{"nullable-uses-first-type", []string{"number", "null"}, "0.5", 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := typedDefault(tc.jsonType, tc.raw)
			if err != nil {
				t.Fatalf("typedDefault(%v, %q) errored: %v", tc.jsonType, tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("typedDefault(%v, %q) = %#v, want %#v", tc.jsonType, tc.raw, got, tc.want)
			}
		})
	}
}

// The weekday enum and the window pattern are read out of config.go so the
// schema can't disagree with the validator. This asserts they arrive, and
// that the pattern still means what the schema claims it means.
func TestParseConfigLiterals(t *testing.T) {
	lits, err := parseConfigLiterals(configSourceFromTest)
	if err != nil {
		t.Fatalf("parseConfigLiterals: %v", err)
	}

	want := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	if len(lits.weekdays) != len(want) {
		t.Fatalf("weekdays = %v, want %v", lits.weekdays, want)
	}
	for i, day := range want {
		if lits.weekdays[i] != day {
			t.Errorf("weekdays[%d] = %q, want %q", i, lits.weekdays[i], day)
		}
	}

	re, err := regexp.Compile(lits.windowPattern)
	if err != nil {
		t.Fatalf("window pattern %q does not compile: %v", lits.windowPattern, err)
	}
	if !re.MatchString("09:00-17:00") {
		t.Errorf("window pattern %q rejects 09:00-17:00", lits.windowPattern)
	}
	if re.MatchString("9:00-17:00") {
		t.Errorf("window pattern %q accepts unpadded 9:00-17:00", lits.windowPattern)
	}
}
