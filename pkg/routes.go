package pkg

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"

	consulapi "github.com/hashicorp/consul/api"
)

func RegisterRoutes(mux *http.ServeMux, consulClient *consulapi.Client, routeMappings string, counter *uint64) {
	routePairs := strings.Split(routeMappings, ",")

	for _, pair := range routePairs {
		parts := strings.Split(strings.TrimSpace(pair), ":")
		if len(parts) == 2 {
			urlPath := strings.TrimSpace(parts[0])
			targetServiceName := strings.TrimSpace(parts[1])

			if strings.HasPrefix(urlPath, "/") {
				if !strings.HasSuffix(urlPath, "/") {
					urlPath = urlPath + "/"
				}
				urlPath = urlPath + "{any...}"
			}

			mux.HandleFunc(urlPath, func(w http.ResponseWriter, r *http.Request) {
				services, _, err := consulClient.Health().Service(targetServiceName, "", true, nil)
				if err != nil || len(services) == 0 {
					Log.Error("Service Unavailable under route cluster",
						"target_service", targetServiceName,
						"path", r.URL.Path,
						"error", err,
					)
					http.Error(w, "Service Unavailable under route cluster", http.StatusServiceUnavailable)
					return
				}

				currentIndex := atomic.AddUint64(counter, 1) % uint64(len(services))
				selectedService := services[currentIndex].Service

				targetUrlStr := fmt.Sprintf("http://%s:%d", selectedService.Address, selectedService.Port)
				targetUrl, err := url.Parse(targetUrlStr)
				if err != nil {
					Log.Error("Failed to parse target URL", "target_url", targetUrlStr, "error", err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}

				Log.Info("PROXY: Routing request to target backend",
					"method", r.Method,
					"path", r.URL.Path,
					"target_host", selectedService.Address,
					"target_port", selectedService.Port,
				)

				proxyEngine := httputil.NewSingleHostReverseProxy(targetUrl)
				r.Header.Set("X-Gateway-Route-Target", targetServiceName)
				proxyEngine.ServeHTTP(w, r)
			})
		}
	}

	// Default local edge diagnostic route
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("{\"status\":\"UP\",\"targets\":\"multi-configured\"}")); err != nil {
			Log.Error("Failed to write health response", "error", err)
		}
	})
}
