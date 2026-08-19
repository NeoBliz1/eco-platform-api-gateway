package pkg

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	consulapi "github.com/hashicorp/consul/api"
)

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

			cache := &routeCache{
				targetService: targetServiceName,
				proxies:       make([]*httputil.ReverseProxy, 0),
			}

			cache.updateTargets(consulClient)

			go func(c *routeCache) {
				ticker := time.NewTicker(10 * time.Second)
				for range ticker.C {
					c.updateTargets(consulClient)
				}
			}(cache)

			mux.HandleFunc(urlPath, func(w http.ResponseWriter, r *http.Request) {
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
				proxy.ServeHTTP(w, r)
			})
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
