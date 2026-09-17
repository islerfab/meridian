package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// configSourceFile and celSourceFile are parsed directly via go/ast —
// consistent with the metrics generator, and for the same reason: no
// runtime dependency on the engine, no risk of a struct literal
// constructing something with side effects.
const (
	configSourceFile = "internal/config/config.go"
	celSourceFile    = "internal/config/cel.go"
)

// configField is one struct field as declared in source, marshaled
// straight to JSON — rendering (headings, details boxes) is Hugo template
// code, not this file's concern.
type configField struct {
	Name     string `json:"name"` // yaml or cel tag name
	Doc      string `json:"doc"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
	Provider string `json:"provider,omitempty"` // "", "google", or "caldav"
	CELType  string `json:"celType,omitempty"`  // set only for the CEL event schema

	celGoType string // Go type as written, e.g. "[]string" — translated to CELType before marshaling
}

// configStruct is one type declaration: its own doc comment (the
// section's lead paragraph, same role as a struct's doc comment in
// rustdoc) plus its fields in declaration order.
type configStruct struct {
	Title  string        `json:"title"`
	Doc    string        `json:"doc"`
	Fields []configField `json:"fields"`
}

func genConfigData() (string, error) {
	structs, err := parseConfigStructs(configSourceFile, map[string]bool{
		"Config": true, "Notifications": true, "Account": true,
		"CalendarConfig": true, "RuleConfig": true, "FilterConfig": true,
		"TransformConfig": true,
	}, true)
	if err != nil {
		return "", err
	}
	// requireDoc=false: CELEvent fields have no doc:"..." tag by design
	// (it's a read-only schema exposed to filter.when, not a config value
	// with a required/optional/default shape) — a bare struct-tag field.
	celStructs, err := parseConfigStructs(celSourceFile, map[string]bool{"CELEvent": true}, false)
	if err != nil {
		return "", err
	}
	celEvent := celStructs["CELEvent"]
	for i := range celEvent.Fields {
		celEvent.Fields[i].CELType = celType(celEvent.Fields[i].celGoType)
	}

	sections := []configStruct{
		withTitle("Top level", structs["Config"]),
		withTitle("`notifications`", structs["Notifications"]),
		withTitle("`accounts`", structs["Account"]),
		withTitle("`accounts[].calendars`", structs["CalendarConfig"]),
		withTitle("`rules`", structs["RuleConfig"]),
		withTitle("`filter`", structs["FilterConfig"]),
		withTitle("`transform`", structs["TransformConfig"]),
	}

	out, err := json.MarshalIndent(struct {
		Sections       []configStruct `json:"sections"`
		CELEventSchema configStruct   `json:"celEventSchema"`
	}{sections, withTitle("CEL event schema", celEvent)}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

func withTitle(title string, s configStruct) configStruct {
	s.Title = title
	return s
}

func celType(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "time.Time":
		return "timestamp"
	case "int64":
		return "int"
	case "bool":
		return "bool"
	case "[]string":
		return "list(string)"
	default:
		return goType
	}
}

// parseConfigStructs walks the given source file's top-level type
// declarations, extracting the doc comment and yaml/cel-tagged fields of
// each named struct in want.
func parseConfigStructs(path string, want map[string]bool, requireDoc bool) (map[string]configStruct, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	found := map[string]configStruct{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !want[ts.Name.Name] {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			doc := ts.Doc.Text()
			if doc == "" {
				doc = gen.Doc.Text() // doc comment sits on the GenDecl for `type X struct {...}` with no parens
			}
			prose, err := deGo(ts.Name.Name, doc)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if err := checkMarkup(path+" "+ts.Name.Name, prose); err != nil {
				return nil, err
			}
			fields, err := parseConfigFields(st, requireDoc)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ts.Name.Name, err)
			}
			found[ts.Name.Name] = configStruct{Doc: prose, Fields: fields}
		}
	}
	for name := range want {
		if _, ok := found[name]; !ok {
			return nil, fmt.Errorf("%s: type %s not found", path, name)
		}
	}
	return found, nil
}

// parseConfigFields walks one struct's fields. requireDoc skips any field
// without a doc:"..." tag — used for the yaml-tagged config structs,
// where an untagged field is a nested struct/slice-of-struct documented
// by its own section (e.g. Config.Notifications), not a leaf value.
// CELEvent's cel-tagged fields never carry a doc tag at all, so their
// caller passes requireDoc=false.
func parseConfigFields(st *ast.StructType, requireDoc bool) ([]configField, error) {
	var fields []configField
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 || !f.Names[0].IsExported() {
			continue
		}
		if f.Tag == nil {
			continue
		}
		tag := parseStructTag(f.Tag.Value)

		yamlName, yamlOK := tag["yaml"]
		celName, celOK := tag["cel"]
		name := yamlName
		if !yamlOK {
			name = celName
		}
		if (!yamlOK && !celOK) || name == "-" {
			continue
		}

		docTag, hasDocTag := tag["doc"]
		if !hasDocTag && requireDoc {
			continue // nested struct documented by its own section, not a leaf value
		}

		prose, err := deGo(f.Names[0].Name, f.Doc.Text())
		if err != nil {
			return nil, err
		}
		if err := checkMarkup(f.Names[0].Name, prose); err != nil {
			return nil, err
		}
		cf := configField{
			Name:      name,
			celGoType: exprString(f.Type),
			Doc:       prose,
		}
		if hasDocTag {
			if err := applyDocTag(&cf, docTag); err != nil {
				return nil, fmt.Errorf("field %s: doc tag %q: %w", f.Names[0].Name, docTag, err)
			}
		}
		fields = append(fields, cf)
	}
	return fields, nil
}

// applyDocTag parses doc:"required|optional[,default=X][,google|caldav]".
func applyDocTag(f *configField, doc string) error {
	parts := strings.Split(doc, ",")
	switch parts[0] {
	case "required":
		f.Required = true
	case "optional":
		f.Required = false
	default:
		return fmt.Errorf("first token must be required or optional, got %q", parts[0])
	}
	for _, p := range parts[1:] {
		switch {
		case strings.HasPrefix(p, "default="):
			f.Default = strings.TrimPrefix(p, "default=")
		case p == "google" || p == "caldav":
			f.Provider = p
		default:
			return fmt.Errorf("unrecognized token %q", p)
		}
	}
	return nil
}

// parseStructTag does the minimal parsing needed for `key:"value"` pairs
// in a raw Go struct tag literal (still including its surrounding
// backticks, as ast.BasicLit.Value preserves them) — avoiding a
// dependency on reflect.StructTag, which needs a real (non-AST) string
// value anyway and offers no advantage here since every tag in this file
// is a simple space-separated key:"value" list.
func parseStructTag(raw string) map[string]string {
	raw = strings.Trim(raw, "`")
	tags := map[string]string{}
	for {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return tags
		}
		colon := strings.IndexByte(raw, ':')
		if colon < 0 {
			return tags
		}
		key := raw[:colon]
		rest := raw[colon+1:]
		if len(rest) == 0 || rest[0] != '"' {
			return tags
		}
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return tags
		}
		tags[key] = rest[1 : 1+end]
		raw = rest[2+end:]
	}
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}
