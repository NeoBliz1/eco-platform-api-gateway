package pkg

import (
	consulapi "github.com/hashicorp/consul/api"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
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
					http.Error(w, "Service Unavailable under route cluster", http.StatusServiceUnavailable)
					return
				}

				currentIndex := atomic.AddUint64(counter, 1) % uint64(len(services))
				selectedService := services[currentIndex].Service

				targetUrlStr := "http://" + selectedService.Address + ":" + string(rune(selectedService.Port))
				targetUrl, err := url.Parse(targetUrlStr)
				if err != nil {
					log.Printf("ERROR: Failed to parse target URL: %v", err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}

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
			log.Printf("ERROR: Failed to write health response: %v", err)
		}
	})
}
