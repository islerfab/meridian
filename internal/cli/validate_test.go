package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validRules = `instance: test
interval: 5m
accounts:
  - name: work
    type: caldav
    endpoint: https://caldav.example.com/dav/
    usernameEnv: U
    passwordEnv: P
    calendars:
      - name: main
        path: /calendars/main/
  - name: personal
    type: google
    clientIDEnv: CI
    clientSecretEnv: CS
    refreshTokenEnv: RT
    calendars:
      - name: main
        id: user@example.com
rules:
  - id: r1
    from: work/main
    to: [personal/main]
    transform:
      title: Busy
`

func TestValidateCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(validRules), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runValidate(t, "--config", path)
	if !strings.Contains(out, "ok: 2 account(s), 1 rule(s)") {
		t.Errorf("output %q missing summary", out)
	}
}

func TestValidateCommandFromConfigMap(t *testing.T) {
	indented := "    " + strings.ReplaceAll(strings.TrimRight(validRules, "\n"), "\n", "\n    ")
	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  rules.yaml: |\n" + indented + "\n"
	path := filepath.Join(t.TempDir(), "cm.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runValidate(t, "--from-configmap", "--config", path)
	if !strings.Contains(out, "ok: 2 account(s), 1 rule(s)") {
		t.Errorf("output %q missing summary", out)
	}
}

func TestValidateCommandRejectsBadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte("interval: 5m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	cmd.Writer = &strings.Builder{}
	err := cmd.Run(context.Background(), []string{"meridian", "validate", "--config", path})
	if err == nil || !strings.Contains(err.Error(), "instance is required") {
		t.Errorf("err = %v, want instance-required validation error", err)
	}
}

func runValidate(t *testing.T, args ...string) string {
	t.Helper()
	var out strings.Builder
	cmd := NewRootCmd()
	cmd.Writer = &out
	if err := cmd.Run(context.Background(), append([]string{"meridian", "validate"}, args...)); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return out.String()
}
