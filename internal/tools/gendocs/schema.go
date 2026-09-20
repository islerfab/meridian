package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// valuesSchemaBase is the hand-written half of the chart's
// values.schema.json: the chart's own plumbing (image, probes, scheduling),
// which has no counterpart in Go source to derive from. The `config` block
// and everything under it is generated from internal/config instead, and
// injected here as `definitions` plus a `properties.config` $ref — so the
// base file on its own is deliberately an incomplete schema.
//
//go:embed values.schema.base.json
var valuesSchemaBase []byte

const valuesSchemaPath = "deploy/meridian/values.schema.json"

// configDefs names the JSON Schema definition generated for each config
// struct. Every struct reachable from Config must appear, or a $ref in the
// generated schema dangles.
var configDefs = map[string]string{
	"Config":          "config",
	"Notifications":   "configNotifications",
	"Guards":          "configGuards",
	"Account":         "configAccount",
	"CalendarConfig":  "configCalendar",
	"RuleConfig":      "configRule",
	"FilterConfig":    "configFilter",
	"TransformConfig": "configTransform",
}

// accountTypes mirrors the provider switch in (*Config).validate. Adding a
// provider without adding it here makes `helm template` reject a valid
// config — loudly, at render time, which is why this is tolerable as a
// hand-kept list while the field names around it are not.
var accountTypes = []string{"caldav", "google"}

func genValuesSchema() (string, error) {
	want := map[string]bool{}
	for goName := range configDefs {
		want[goName] = true
	}
	// requireDoc=false: unlike the docs page, the schema needs the fields
	// that carry no doc tag — notifications, accounts, rules, calendars are
	// exactly the nesting the $refs are built from.
	structs, err := parseConfigStructs(configSourceFile, want, false)
	if err != nil {
		return "", err
	}
	lits, err := parseConfigLiterals(configSourceFile)
	if err != nil {
		return "", err
	}

	defs := map[string]any{}
	for goName, defName := range configDefs {
		props := map[string]any{}
		for _, f := range structs[goName].Fields {
			node, err := schemaForField(goName, f, lits)
			if err != nil {
				return "", fmt.Errorf("%s.%s: %w", goName, f.Name, err)
			}
			props[f.Name] = node
		}
		def := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           props,
		}
		if doc := structs[goName].Doc; doc != "" {
			def["description"] = doc
		}
		defs[defName] = def
	}

	var root map[string]any
	if err := json.Unmarshal(valuesSchemaBase, &root); err != nil {
		return "", fmt.Errorf("values.schema.base.json: %w", err)
	}
	props, ok := root["properties"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("values.schema.base.json: no properties object")
	}
	props["config"] = map[string]any{
		"$ref":        "#/definitions/" + configDefs["Config"],
		"description": "Meridian's rules.yaml, rendered verbatim into a ConfigMap. Ignored when existingConfigMap is set. Required fields and cross-field rules are validated by meridian at startup, not here.",
	}
	root["definitions"] = defs

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

// schemaForField translates one config struct field into a JSON Schema
// node. Requiredness is deliberately absent: the chart ships empty
// placeholders for instance, accounts and rules, so a schema that demanded
// them would reject the chart's own defaults — and internal/config reports
// a missing field far better than a schema can.
func schemaForField(structName string, f configField, lits configLiterals) (map[string]any, error) {
	node, err := jsonTypeFor(f.celGoType)
	if err != nil {
		return nil, err
	}
	if f.Doc != "" {
		node["description"] = f.Doc
	}

	switch structName + "." + f.Name {
	case "Account.type":
		node["enum"] = accountTypes
	case "FilterConfig.weekdays":
		items, ok := node["items"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("want an array node to constrain, got %v", node["type"])
		}
		items["enum"] = lits.weekdays
	case "TransformConfig.visibility":
		node["enum"] = lits.visibilities
	case "FilterConfig.window":
		node["pattern"] = lits.windowPattern
	case "Guards.massDeleteFraction":
		node["minimum"] = 0
		node["maximum"] = 1
	}

	if f.Default != "" {
		def, err := typedDefault(node["type"], f.Default)
		if err != nil {
			return nil, fmt.Errorf("default %q: %w", f.Default, err)
		}
		node["default"] = def
	}
	return node, nil
}

// jsonTypeFor maps a Go type as written in config.go to a JSON Schema type
// node. Pointer types accept null as well: a pointer is how config.go says
// "unset is distinguishable from the zero value", which in YAML is a bare
// `key:` with nothing after it.
func jsonTypeFor(goType string) (map[string]any, error) {
	if defName, ok := configDefs[trimPointer(goType)]; ok {
		return map[string]any{"$ref": "#/definitions/" + defName}, nil
	}
	if elem, ok := sliceElem(goType); ok {
		items, err := jsonTypeFor(elem)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	}

	scalar := ""
	switch trimPointer(goType) {
	case "string":
		scalar = "string"
	case "bool":
		scalar = "boolean"
	case "int":
		scalar = "integer"
	case "float64":
		scalar = "number"
	default:
		return nil, fmt.Errorf("no JSON Schema mapping for Go type %q", goType)
	}
	if goType != trimPointer(goType) {
		return map[string]any{"type": []string{scalar, "null"}}, nil
	}
	return map[string]any{"type": scalar}, nil
}

func trimPointer(goType string) string {
	if len(goType) > 0 && goType[0] == '*' {
		return goType[1:]
	}
	return goType
}

func sliceElem(goType string) (string, bool) {
	if len(goType) > 2 && goType[:2] == "[]" {
		return goType[2:], true
	}
	return "", false
}

// typedDefault converts a doc tag's default (always written as a string)
// into the JSON type the field actually holds, so editors show `false`
// rather than `"false"` on hover.
func typedDefault(jsonType any, raw string) (any, error) {
	name, _ := jsonType.(string)
	if names, ok := jsonType.([]string); ok && len(names) > 0 {
		name = names[0]
	}
	switch name {
	case "boolean":
		return strconv.ParseBool(raw)
	case "number", "integer":
		return strconv.ParseFloat(raw, 64)
	default:
		return raw, nil
	}
}

// configLiterals holds the validation constants read straight out of
// config.go, so the schema cannot disagree with the validator about what a
// weekday or a window looks like.
type configLiterals struct {
	weekdays      []string
	visibilities  []string
	windowPattern string
}

func parseConfigLiterals(path string) (configLiterals, error) {
	var lits configLiterals
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return lits, fmt.Errorf("parsing %s: %w", path, err)
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			switch vs.Names[0].Name {
			case "weekdayNames":
				lits.weekdays, err = compositeLitKeys(vs.Values[0])
			case "visibilityNames":
				lits.visibilities, err = compositeLitKeys(vs.Values[0])
			case "windowRe":
				lits.windowPattern, err = mustCompileArg(vs.Values[0])
			default:
				continue
			}
			if err != nil {
				return lits, fmt.Errorf("%s: %s: %w", path, vs.Names[0].Name, err)
			}
		}
	}

	if len(lits.weekdays) == 0 {
		return lits, fmt.Errorf("%s: weekdayNames not found", path)
	}
	if len(lits.visibilities) == 0 {
		return lits, fmt.Errorf("%s: visibilityNames not found", path)
	}
	if lits.windowPattern == "" {
		return lits, fmt.Errorf("%s: windowRe not found", path)
	}
	return lits, nil
}

// compositeLitKeys returns the string keys of a map literal, in
// declaration order.
func compositeLitKeys(e ast.Expr) ([]string, error) {
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("want a composite literal, got %T", e)
	}
	var keys []string
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, fmt.Errorf("want key: value elements, got %T", elt)
		}
		key, err := stringLit(kv.Key)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// mustCompileArg returns the pattern passed to regexp.MustCompile(...).
func mustCompileArg(e ast.Expr) (string, error) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", fmt.Errorf("want a single-argument call, got %T", e)
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "MustCompile" {
		return "", fmt.Errorf("want regexp.MustCompile")
	}
	return stringLit(call.Args[0])
}
