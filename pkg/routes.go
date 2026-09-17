package pkg

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

var (
	validServiceName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	validRoutePath    = regexp.MustCompile(`^/[a-z0-9/_{}:.-]*$`)
	sqlInjectionRegex = regexp.MustCompile(`(?i)(select|drop|insert|delete|update|union|alter|--|#)`)
	xssInjectionRegex = regexp.MustCompile(`(?i)(<script|javascript:|onerror|onload)`)
)

// ValidateRouteRule executes on application startup to validate static routing configuration entries.
func ValidateRouteRule(index int, urlPath, targetService string) bool {
	if !strings.HasPrefix(urlPath, "/") {
		Log.Warn("Skipping route: path must start with '/'", "index", index, "path", urlPath)
		return false
	}
	if !validRoutePath.MatchString(urlPath) {
		Log.Warn("Skipping route: path contains invalid characters", "index", index, "path", urlPath)
		return false
	}
	if !validServiceName.MatchString(targetService) {
		Log.Warn("Skipping route: invalid service name format", "index", index, "service", targetService)
		return false
	}
	return true
}

// SanitizeAndValidateQuery cleans runtime query strings and isolates malicious payloads.
func SanitizeAndValidateQuery(rawQuery string) (string, bool) {
	if len(rawQuery) > 1024 {
		Log.Warn("Query validation failed: parameter string exceeds 1024 bytes", "length", len(rawQuery))
		return "", false
	}

	decodedQuery, err := url.QueryUnescape(rawQuery)
	if err != nil {
		Log.Warn("Query validation failed: malformed unescape encoding", "error", err)
		return "", false
	}

	if sqlInjectionRegex.MatchString(decodedQuery) {
		Log.Warn("Security alert: blocked potential SQL injection string", "query", decodedQuery)
		return "", false
	}

	if xssInjectionRegex.MatchString(decodedQuery) {
		Log.Warn("Security alert: blocked potential XSS payload", "query", decodedQuery)
		return "", false
	}

	if strings.Contains(decodedQuery, "../") || strings.Contains(decodedQuery, "..\\") {
		Log.Warn("Security alert: blocked potential directory traversal path", "query", decodedQuery)
		return "", false
	}

	return rawQuery, true
}

func RegisterRoutes(mux *http.ServeMux, consulClient *consulapi.Client, routeMappings string) {
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("{\"status\":\"UP\",\"targets\":\"multi-configured\"}")); err != nil {
			Log.Error("Failed to write health response", "error", err)
		}
	})

	routePairs := strings.Split(routeMappings, ",")

	for i, pair := range routePairs {
		parts := strings.Split(strings.TrimSpace(pair), ":")
		if len(parts) != 2 {
			Log.Warn("Skipping invalid route entry: expected 'path:service' format", "index", i, "entry", pair)
			continue
		}

		urlPath := strings.TrimSpace(parts[0])
		targetServiceName := strings.TrimSpace(parts[1])

		// Perform startup validation checks on parsed rules
		if !ValidateRouteRule(i, urlPath, targetServiceName) {
			continue
		}

		cache := &routeCache{
			targetService: targetServiceName,
			proxies:       make([]*httputil.ReverseProxy, 0),
		}

		cache.updateTargets(consulClient)

		go func(c *routeCache) {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				c.updateTargets(consulClient)
			}
		}(cache)

		proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, urlPath) {
				Log.Warn("Exploit blocked: Path routing mismatch inside closure", "expected", urlPath, "got", r.URL.Path)
				http.Error(w, "Access Denied: Routing Mismatch", http.StatusForbidden)
				return
			}
			sanitizedQuery, ok := SanitizeAndValidateQuery(r.URL.RawQuery)
			if !ok {
				http.Error(w, "Query validation failed or contains illegal characters", http.StatusBadRequest)
				return
			}
			r.URL.RawQuery = sanitizedQuery

			cache.mu.RLock()
			proxiesLen := len(cache.proxies)

			if proxiesLen == 0 {
				cache.mu.RUnlock()
				Log.Error("Service Unavailable under route cluster", "target_service", cache.targetService, "path", r.URL.Path)
				http.Error(w, "Service Unavailable under route cluster", http.StatusServiceUnavailable)
				return
			}

			currentIndex := atomic.AddUint64(&cache.counter, 1) % uint64(proxiesLen)
			proxy := cache.proxies[currentIndex]
			cache.mu.RUnlock()

			r.Header.Set("X-Gateway-Route-Target", cache.targetService)
			wrappedWriter := &StatusResponseWriter{ResponseWriter: w, StatusCode: http.StatusOK}
			InjectTraceContext(r)
			proxy.ServeHTTP(wrappedWriter, r)

			Log.Debug("Intercepted Reverse Proxy Response Status",
				"target_service", cache.targetService,
				"status_code", wrappedWriter.StatusCode,
				"url", r.URL.Path,
			)
		})

		// Register routes as exact paths and directory path prefixes.
		// This passes standard query strings seamlessly without breaking the route patterns.
		mux.Handle(urlPath, WrapWithTracing(proxyHandler, targetServiceName))
		if !strings.HasSuffix(urlPath, "/") {
			mux.Handle(urlPath+"/", WrapWithTracing(proxyHandler, targetServiceName))
		}
	}
}

func (c *routeCache) updateTargets(consulClient *consulapi.Client) {
	services, _, err := consulClient.Health().Service(c.targetService, "", true, nil)
	if err != nil {
		Log.Error("Failed to fetch healthy instances from Consul", "service", c.targetService, "error", err)
		return
	}

	var activeProxies []*httputil.ReverseProxy

	for _, entry := range services {
		targetUrlStr := fmt.Sprintf("http://%s:%d", entry.Service.Address, entry.Service.Port)
		targetUrl, err := url.Parse(targetUrlStr)
		if err != nil {
			Log.Error("Failed to parse backend instance URL", "url", targetUrlStr, "error", err)
			continue
		}

		proxy := httputil.NewSingleHostReverseProxy(targetUrl)
		activeProxies = append(activeProxies, proxy)
	}

	c.mu.Lock()
	c.proxies = activeProxies
	c.mu.Unlock()

	Log.Debug("Route registry endpoints updated", "service", c.targetService, "healthy_count", len(activeProxies))
}

func (w *StatusResponseWriter) WriteHeader(code int) {
	w.StatusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func WrapWithTracing(handler http.Handler, targetService string) http.Handler {
	spanName := fmt.Sprintf("Proxy_To_%s", targetService)
	return otelhttp.NewHandler(handler, spanName)
}

func InjectTraceContext(r *http.Request) {
	ctx := r.Context()
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(r.Header))
}
