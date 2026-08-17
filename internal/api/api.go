// Package api implements the Aegis Sample API's HTTP surface: health,
// readiness, metrics, and one small piece of deterministic application
// logic used to exercise Aegis's operating model (routing, metrics,
// policy) with real traffic rather than only static health responses.
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Version and Commit are set at build time via -ldflags; left at these
// defaults for local runs. Commit is the full Git SHA the release was
// built from — useful once a running Pod's version alone isn't enough to
// tell which exact source state produced it.
var (
	Version = "dev"
	Commit  = "unknown"
)

// maxFibonacciN bounds /api/v1/work so the response stays deterministic
// and the computation stays O(n) with no risk of overflow: fib(40) is
// well within int64, fib(93) is the last one that fits at all.
const maxFibonacciN = 40

const contentTypeJSON = "application/json"

// Server holds the application's runtime state: metrics registry and
// readiness flag. Liveness has no equivalent state — a process that can
// still handle a liveness check is by definition alive.
type Server struct {
	ready atomic.Bool

	gatherer        prometheus.Gatherer
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	workRequests    prometheus.Counter
}

// NewServer wires metrics into the given registry rather than the global
// default, so /metrics serves exactly what this Server registered and
// tests can construct independent instances without collector
// registration collisions.
func NewServer(reg *prometheus.Registry) *Server {
	s := &Server{
		gatherer: reg,
		requestsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "aegis_api_http_requests_total",
			Help: "Total HTTP requests by method, path and status code.",
		}, []string{"method", "path", "status"}),
		requestDuration: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aegis_api_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds by method and path.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path"}),
		workRequests: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Name: "aegis_api_work_requests_total",
			Help: "Total accepted /api/v1/work requests.",
		}),
	}
	return s
}

// SetReady flips readiness. Called true once startup warm-up completes,
// and false during shutdown so Kubernetes stops routing new requests
// before the process actually exits.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
	log.Printf("readiness changed: ready=%v", ready)
}

// Handler returns the application's full route table, wrapped with a
// metrics-recording middleware. A plain http.ServeMux is enough for four
// routes; a router dependency would be answering a problem this API
// doesn't have.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", s.instrument("/healthz", http.HandlerFunc(s.handleHealthz)))
	mux.Handle("/readyz", s.instrument("/readyz", http.HandlerFunc(s.handleReadyz)))
	mux.Handle("/api/v1/info", s.instrument("/api/v1/info", http.HandlerFunc(s.handleInfo)))
	mux.Handle("/api/v1/work", s.instrument("/api/v1/work", http.HandlerFunc(s.handleWork)))
	mux.Handle("/metrics", promhttp.HandlerFor(s.gatherer, promhttp.HandlerOpts{}))
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) instrument(path string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(rec.status)
		s.requestsTotal.WithLabelValues(r.Method, path, status).Inc()
		s.requestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}

// handleHealthz answers "is this process alive" only. It does not check
// readiness state, so a Kubernetes liveness probe never restarts a pod
// merely because it hasn't finished warming up.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleReadyz answers "should this pod receive traffic right now". It is
// intentionally a separate signal from healthz: today the only readiness
// input is startup warm-up and shutdown draining, but the split is what
// lets a future real dependency (should one ever be added) affect routing
// without also triggering process restarts.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", contentTypeJSON)
	if !s.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"not ready"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

type infoResponse struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Environment string `json:"environment"`
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	env := "development"
	resp := infoResponse{Name: "aegis-api", Version: Version, Commit: Commit, Environment: env}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

type workResponse struct {
	Input  int64 `json:"input"`
	Result int64 `json:"result"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// handleWork computes fib(n) for a bounded, validated n. Fibonacci was
// chosen over a trivial transform (like squaring) because its result
// depends on genuine iteration, not just echoing the input back
// arithmetically — closer to real application work while staying
// deterministic and side-effect-free.
func (s *Server) handleWork(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", contentTypeJSON)

	raw := r.URL.Query().Get("value")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing required query parameter: value")
		return
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "value must be an integer")
		return
	}
	if n < 0 || n > maxFibonacciN {
		writeError(w, http.StatusBadRequest, "value must be between 0 and 40 inclusive")
		return
	}

	s.workRequests.Inc()
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(workResponse{Input: n, Result: fibonacci(n)})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

func fibonacci(n int64) int64 {
	if n < 2 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}
