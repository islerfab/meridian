// Package config parses and validates rules.yaml: typed filter/transform
// fields, CEL `when` predicates compiled and type-checked at startup, and
// Go templates for string transforms.
//
// Invariants: docs/content/design.md, "The rule".
package config
