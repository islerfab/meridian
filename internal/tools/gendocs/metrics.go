package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// metricsSourceFile is parsed directly (rather than importing the sync
// package and calling NewMetrics) so generation has no runtime
// dependency on the engine and can never register real collectors.
const metricsSourceFile = "internal/sync/metrics.go"

// promMetric is one metric as declared in metricsSourceFile, marshaled
// straight to JSON: its prometheus.XxxOpts Name/Help are read verbatim,
// so the reference page can't say anything about a metric that the
// metric itself doesn't already say via its Help text (the same text
// Prometheus/Grafana show operators today). Rendering is Hugo template
// code, not this file's concern.
type promMetric struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"` // counter | gauge | histogram
	Labels []string `json:"labels,omitempty"`
	Help   string   `json:"help"`
}

func genMetricsData() (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, metricsSourceFile, nil, 0)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", metricsSourceFile, err)
	}

	var metrics []promMetric
	var walkErr error
	ast.Inspect(file, func(n ast.Node) bool {
		if walkErr != nil {
			return false
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if ident, ok := lit.Type.(*ast.Ident); !ok || ident.Name != "Metrics" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			m, err := parseMetricValue(kv.Value)
			if err != nil {
				walkErr = fmt.Errorf("field %s: %w", exprString(kv.Key), err)
				return false
			}
			metrics = append(metrics, m)
		}
		return false
	})
	if walkErr != nil {
		return "", walkErr
	}
	if len(metrics) == 0 {
		return "", fmt.Errorf("%s: found no Metrics{...} literal to extract from", metricsSourceFile)
	}

	out, err := json.MarshalIndent(struct {
		Metrics []promMetric `json:"metrics"`
	}{metrics}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

// parseMetricValue expects a call like:
//
//	prometheus.NewCounterVec(prometheus.CounterOpts{Name: "...", Help: "..."}, []string{"a", "b"})
//	prometheus.NewHistogram(prometheus.HistogramOpts{Name: "...", Help: "...", Buckets: ...})
func parseMetricValue(v ast.Expr) (promMetric, error) {
	call, ok := v.(*ast.CallExpr)
	if !ok {
		return promMetric{}, fmt.Errorf("expected a prometheus.NewXxx(...) call, got %T", v)
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return promMetric{}, fmt.Errorf("expected a qualified prometheus.NewXxx call, got %T", call.Fun)
	}
	metricType := metricTypeOf(sel.Sel.Name)
	if metricType == "" {
		return promMetric{}, fmt.Errorf("unrecognized constructor %q", sel.Sel.Name)
	}
	if len(call.Args) == 0 {
		return promMetric{}, fmt.Errorf("%s: no arguments", sel.Sel.Name)
	}
	optsLit, ok := call.Args[0].(*ast.CompositeLit)
	if !ok {
		return promMetric{}, fmt.Errorf("%s: expected an Opts composite literal argument, got %T", sel.Sel.Name, call.Args[0])
	}
	m := promMetric{Type: metricType}
	for _, elt := range optsLit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			s, err := stringLit(kv.Value)
			if err != nil {
				return promMetric{}, fmt.Errorf("name: %w", err)
			}
			m.Name = s
		case "Help":
			s, err := stringLit(kv.Value)
			if err != nil {
				return promMetric{}, fmt.Errorf("help: %w", err)
			}
			m.Help = s
		}
	}
	if m.Name == "" {
		return promMetric{}, fmt.Errorf("%s: no Name field", sel.Sel.Name)
	}
	if len(call.Args) > 1 {
		labels, err := stringSliceLit(call.Args[1])
		if err != nil {
			return promMetric{}, fmt.Errorf("%s: labels argument: %w", m.Name, err)
		}
		m.Labels = labels
	}
	return m, nil
}

func metricTypeOf(constructor string) string {
	switch constructor {
	case "NewCounter", "NewCounterVec":
		return "counter"
	case "NewGauge", "NewGaugeVec":
		return "gauge"
	case "NewHistogram", "NewHistogramVec":
		return "histogram"
	default:
		return ""
	}
}

func stringLit(e ast.Expr) (string, error) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("expected a string literal, got %T", e)
	}
	return strconv.Unquote(lit.Value)
}

func stringSliceLit(e ast.Expr) ([]string, error) {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("expected a []string{...} literal, got %T", e)
	}
	labels := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		s, err := stringLit(elt)
		if err != nil {
			return nil, err
		}
		labels = append(labels, s)
	}
	return labels, nil
}
