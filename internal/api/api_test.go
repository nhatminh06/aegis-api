package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func newTestServer() *Server {
	return NewServer(prometheus.NewRegistry())
}

func TestHealthzAlwaysOK(t *testing.T) {
	s := newTestServer()
	// Deliberately not marked ready: healthz must not depend on readiness,
	// or a liveness probe would restart a pod that is merely warming up.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestReadyzReflectsState(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before ready, got %d", rec.Code)
	}

	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after ready, got %d", rec.Code)
	}

	s.SetReady(false)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after readiness withdrawn, got %d", rec.Code)
	}
}

func TestInfoStructure(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got infoResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Name != "aegis-api" {
		t.Errorf("expected name=aegis-api, got %q", got.Name)
	}
	if got.Environment == "" {
		t.Error("expected non-empty environment")
	}
}

func TestWorkDeterministic(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"0", 0},
		{"1", 1},
		{"2", 1},
		{"10", 55},
		{"20", 6765},
		{"40", 102334155},
	}
	for _, tc := range cases {
		s := newTestServer()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/work?value="+tc.input, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("value=%s: expected 200, got %d (%s)", tc.input, rec.Code, rec.Body.String())
		}
		var got workResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("value=%s: decoding response: %v", tc.input, err)
		}
		if got.Result != tc.want {
			t.Errorf("value=%s: expected result=%d, got %d", tc.input, tc.want, got.Result)
		}
	}
}

func TestWorkInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"missing value", ""},
		{"not an integer", "value=abc"},
		{"negative", "value=-1"},
		{"exceeds bound", "value=41"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer()
			url := "/api/v1/work"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)

			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("expected a 4xx status, got %d", rec.Code)
			}
			var got errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decoding error response: %v", err)
			}
			if got.Error == "" {
				t.Error("expected a non-empty error message")
			}
		})
	}
}

func TestMetricsExposed(t *testing.T) {
	s := newTestServer()
	// Generate one request of real traffic so the counter has something
	// to report, rather than asserting only that the endpoint exists.
	workReq := httptest.NewRequest(http.MethodGet, "/api/v1/work?value=5", nil)
	s.Handler().ServeHTTP(httptest.NewRecorder(), workReq)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"aegis_api_http_requests_total",
		"aegis_api_work_requests_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected metrics output to contain %q", want)
		}
	}
}
