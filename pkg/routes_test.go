package pkg

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	consulapi "github.com/hashicorp/consul/api"
)

func TestValidateRouteRule(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		service string
		want    bool
	}{
		{"valid route", "/api/v1/telemetry/", "eco-monitoring-ingestion", true},
		{"valid route with underscore", "/api/v1/tx_history", "history_service", true},
		{"path without leading slash", "api/v1/telemetry", "eco-monitoring-ingestion", false},
		{"path with uppercase", "/API/v1", "eco-monitoring-ingestion", false},
		{"path with space", "/api/v1/ telemetry", "eco-monitoring-ingestion", false},
		{"service with uppercase", "/api/v1/", "Eco-Ingestion", false},
		{"service starting with dash", "/api/v1/", "-ingestion", false},
		{"empty service", "/api/v1/", "", false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateRouteRule(i, tt.path, tt.service); got != tt.want {
				t.Errorf("ValidateRouteRule(%q, %q) = %v, want %v", tt.path, tt.service, got, tt.want)
			}
		})
	}
}

func TestSanitizeAndValidateQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"empty query", "", true},
		{"plain parameters", "lat=55.75&lon=37.61&radius=10", true},
		{"encoded value", "station=north%20field", true},
		{"sql injection", "id=1%20UNION%20SELECT%20password", false},
		{"sql comment", "id=1--", false},
		{"xss script tag", "q=%3Cscript%3Ealert(1)%3C/script%3E", false},
		{"xss javascript uri", "redirect=javascript:alert(1)", false},
		{"path traversal", "file=../../etc/passwd", false},
		{"malformed encoding", "q=%zz", false},
		{"too long", "q=" + strings.Repeat("a", 1100), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SanitizeAndValidateQuery(tt.query)
			if ok != tt.want {
				t.Fatalf("SanitizeAndValidateQuery(%q) ok = %v, want %v", tt.query, ok, tt.want)
			}
			if ok && got != tt.query {
				t.Errorf("valid query was modified: got %q, want %q", got, tt.query)
			}
		})
	}
}

// newFakeConsul serves the Consul health endpoint, reporting the given backends
// as healthy instances of every requested service.
func newFakeConsul(t *testing.T, backends ...*httptest.Server) *consulapi.Client {
	t.Helper()
	var entries []map[string]any
	for _, b := range backends {
		u, _ := url.Parse(b.URL)
		port, _ := strconv.Atoi(u.Port())
		entries = append(entries, map[string]any{
			"Service": map[string]any{"Address": u.Hostname(), "Port": port},
		})
	}
	consul := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/health/service/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}))
	t.Cleanup(consul.Close)

	cfg := consulapi.DefaultConfig()
	cfg.Address = strings.TrimPrefix(consul.URL, "http://")
	client, err := consulapi.NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create consul client: %v", err)
	}
	return client
}

func newBackend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s|%s|%s", name, r.URL.Path, r.Header.Get("X-Gateway-Route-Target"))
	}))
	t.Cleanup(b.Close)
	return b
}

func doGet(t *testing.T, handler http.Handler, target string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestRegisterRoutes_RoundRobinAcrossHealthyInstances(t *testing.T) {
	consul := newFakeConsul(t, newBackend(t, "a"), newBackend(t, "b"))
	mux := http.NewServeMux()
	RegisterRoutes(mux, consul, "/api/v1/telemetry/:eco-monitoring-ingestion")

	hits := map[string]int{}
	for i := 0; i < 10; i++ {
		code, body := doGet(t, mux, "/api/v1/telemetry/mono")
		if code != http.StatusOK {
			t.Fatalf("request %d: status = %d, body = %q", i, code, body)
		}
		parts := strings.Split(body, "|")
		if parts[1] != "/api/v1/telemetry/mono" {
			t.Errorf("backend got path %q, want /api/v1/telemetry/mono", parts[1])
		}
		if parts[2] != "eco-monitoring-ingestion" {
			t.Errorf("X-Gateway-Route-Target = %q, want eco-monitoring-ingestion", parts[2])
		}
		hits[parts[0]]++
	}
	if hits["a"] != 5 || hits["b"] != 5 {
		t.Errorf("requests not balanced evenly across instances: %v", hits)
	}
}

func TestRegisterRoutes_NoHealthyInstances(t *testing.T) {
	consul := newFakeConsul(t) // no backends
	mux := http.NewServeMux()
	RegisterRoutes(mux, consul, "/api/v1/analysis/:eco-monitoring-analysis")

	if code, _ := doGet(t, mux, "/api/v1/analysis/spatial"); code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
}

func TestRegisterRoutes_RejectsMaliciousQuery(t *testing.T) {
	consul := newFakeConsul(t, newBackend(t, "a"))
	mux := http.NewServeMux()
	RegisterRoutes(mux, consul, "/api/v1/telemetry/:eco-monitoring-ingestion")

	if code, _ := doGet(t, mux, "/api/v1/telemetry/mono?id=1%20UNION%20SELECT%201"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
}

func TestRegisterRoutes_SkipsInvalidRules(t *testing.T) {
	consul := newFakeConsul(t, newBackend(t, "a"))
	mux := http.NewServeMux()
	RegisterRoutes(mux, consul, "no-colon-entry, BAD/:svc ,/api/v1/ok/:eco-ok")

	if code, _ := doGet(t, mux, "/api/v1/ok/x"); code != http.StatusOK {
		t.Errorf("valid route: status = %d, want 200", code)
	}
	if code, _ := doGet(t, mux, "/BAD/x"); code != http.StatusNotFound {
		t.Errorf("invalid route was registered: status = %d, want 404", code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, newFakeConsul(t), "")

	code, body := doGet(t, mux, "/health")
	if code != http.StatusOK || !strings.Contains(body, `"status":"UP"`) {
		t.Errorf("GET /health = %d %q", code, body)
	}
}
