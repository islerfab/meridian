package obs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestServerEndpoints(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "meridian_test_total", Help: "test"})
	reg.MustRegister(c)
	c.Inc()

	s := New("127.0.0.1:0", reg, slog.Default())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background()) //nolint:errcheck
	base := "http://" + s.Addr()

	if code, _ := get(t, base+"/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", code)
	}
	if code, _ := get(t, base+"/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("readyz before SetReady = %d, want 503", code)
	}
	s.SetReady()
	if code, _ := get(t, base+"/readyz"); code != http.StatusOK {
		t.Errorf("readyz after SetReady = %d, want 200", code)
	}
	code, body := get(t, base+"/metrics")
	if code != http.StatusOK {
		t.Errorf("metrics = %d, want 200", code)
	}
	if !strings.Contains(body, "meridian_test_total 1") {
		t.Errorf("metrics body missing registered counter:\n%s", body)
	}
}

func TestStartBindFailureIsError(t *testing.T) {
	s := New("127.0.0.1:0", prometheus.NewRegistry(), slog.Default())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background()) //nolint:errcheck

	dup := New(s.Addr(), prometheus.NewRegistry(), slog.Default())
	if err := dup.Start(); err == nil {
		defer dup.Shutdown(context.Background()) //nolint:errcheck
		t.Fatal("Start() on an occupied port = nil, want bind error")
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}
