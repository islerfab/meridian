// Package obs serves the observability HTTP surface: /metrics (Prometheus),
// /healthz (liveness: process up), /readyz (readiness: config parsed, CEL
// compiled, auth probes passed). Readiness latches — once the startup auth
// probes pass it never flips back; later staleness is Prometheus's job
// (last_successful_cycle alert), not the kubelet's.
package obs

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server is the observability endpoint, serving all three paths on one
// listener. Bind [::] for dual-stack clusters.
type Server struct {
	ready atomic.Bool
	http  *http.Server
	ln    net.Listener
	log   *slog.Logger
}

// New builds the server; Start binds it.
func New(addr string, gatherer prometheus.Gatherer, log *slog.Logger) *Server {
	s := &Server{log: log}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.ready.Load() {
			http.Error(w, "not ready: startup auth probes have not passed", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.http.Addr = addr
	return s
}

// Start binds synchronously — a bind failure is a startup error — then
// serves in the background.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("observability server failed", "err", err)
		}
	}()
	return nil
}

// Addr is the bound address (resolves :0 in tests).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// SetReady latches readiness.
func (s *Server) SetReady() { s.ready.Store(true) }

// Shutdown drains in-flight requests until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }
