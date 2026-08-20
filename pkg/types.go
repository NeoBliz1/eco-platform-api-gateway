package pkg

import (
	"net/http"
	"net/http/httputil"
	"sync"
)

type Config struct {
	ConsulAddress          string `envconfig:"CONSUL_ADDRESS"`
	GatewayPort            string `yaml:"port"`
	ServerHost             string `yaml:"host"`
	RouteMappings          string `yaml:"routes"`
	LogLevel               string `yaml:"log_level"`
	LogstashTcpDestination string `yaml:"logstash_destination"`
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
