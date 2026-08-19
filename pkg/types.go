package pkg

import (
	"net/http"
	"net/http/httputil"
	"sync"
)

type Config struct {
	GatewayPort            string `envconfig:"GATEWAY_PORT"`
	ConsulAddress          string `envconfig:"CONSUL_ADDRESS"`
	RouteMappings          string `envconfig:"ROUTE_MAPPINGS"`
	ServerHost             string `envconfig:"SERVER_HOST"`
	LogstashTcpDestination string `envconfig:"LOGSTASH_TCP_DESTINATION"`
	LogLevel               string `envconfig:"LOG_LEVEL"`
}

type routeCache struct {
	targetService string
	counter       uint64
	mu            sync.RWMutex
	proxies       []*httputil.ReverseProxy
}

type StatusResponseWriter struct {
	http.ResponseWriter
	StatusCode int
}

func (rw *StatusResponseWriter) WriteHeader(code int) {
	rw.StatusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
