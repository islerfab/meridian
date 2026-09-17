package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpsertEnvKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	// Creates the file when missing.
	if err := upsertEnvKey(path, "KEY", "v1"); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "KEY=\"v1\"\n")

	// Preserves other lines and comments, replaces in place.
	seed := "# comment\nOTHER=x\nKEY=\"v1\"\nexport TRAILING=y\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnvKey(path, "KEY", "v2"); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "# comment\nOTHER=x\nKEY=\"v2\"\nexport TRAILING=y\n")

	// Replaces `export KEY=` style too.
	if err := os.WriteFile(path, []byte("export KEY=\"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnvKey(path, "KEY", "new"); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "KEY=\"new\"\n")

	// Does not touch keys that merely share a prefix.
	if err := os.WriteFile(path, []byte("KEY_EXTENDED=z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upsertEnvKey(path, "KEY", "v3"); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "KEY_EXTENDED=z\nKEY=\"v3\"\n")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("env file mode = %o, want 600", perm)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("file content:\n%q\nwant:\n%q", got, want)
	}
}

func TestWaitForCallback(t *testing.T) {
	const port = 55556
	const state = "test-state"

	type outcome struct {
		code string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		code, err := waitForCallback(context.Background(), port, state, "http://unused", &strings.Builder{})
		done <- outcome{code, err}
	}()

	// Wait for the listener (probing a non-callback path so the flow is
	// untouched), then simulate the browser redirect. A wrong state must
	// be rejected without completing the flow.
	probe := fmt.Sprintf("http://localhost:%d/", port)
	url := fmt.Sprintf("http://localhost:%d/callback", port)
	waitListening(t, probe)
	resp, err := http.Get(url + "?state=wrong&code=evil")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("wrong state: status %d, want 400", resp.StatusCode)
	}
	o := <-done
	if o.err == nil {
		t.Fatal("state mismatch must fail the flow")
	}

	// Fresh flow: correct state delivers the code.
	go func() {
		code, err := waitForCallback(context.Background(), port, state, "http://unused", &strings.Builder{})
		done <- outcome{code, err}
	}()
	waitListening(t, probe)
	resp, err = http.Get(url + "?state=" + state + "&code=authcode123")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	o = <-done
	if o.err != nil || o.code != "authcode123" {
		t.Errorf("callback: code=%q err=%v, want authcode123", o.code, o.err)
	}
}

func waitListening(t *testing.T, url string) {
	t.Helper()
	for range 100 {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("callback listener never came up")
}
