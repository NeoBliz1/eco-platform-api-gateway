package api

import (
	"context"
	"eco-platform-api-gateway/pkg"
	"eco-platform-api-gateway/pkg/telemetry"
	"fmt"
	consulapi "github.com/hashicorp/consul/api"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func StartGateway(config *pkg.Config) {
	var otelShutdown func(context.Context) error

	if config.RequestTraceEnabled {
		pkg.Log.Info("Initializing global OpenTelemetry distributed tracer targeting Jaeger...")
		initCtx, initCancel := context.WithTimeout(context.Background(), 5*time.Second)

		var err error
		otelShutdown, err = telemetry.InitTracer(initCtx, "gateway-service")
		initCancel()
		if err != nil {
			pkg.Log.Error("CRITICAL: OpenTelemetry trace pipeline failed to start", "error", err)
		}
	} else {
		pkg.Log.Info("OpenTelemetry trace pipeline disabled via configuration (request_trace_enabled = false).")
	}

	defer func() {
		if otelShutdown != nil {
			pkg.Log.Info("Flushing telemetry batches to Jaeger backend...")
			shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutCancel()
			if shutErr := otelShutdown(shutCtx); shutErr != nil {
				pkg.Log.Warn("Telemetry buffer flush error encountered during cleanup", "error", shutErr)
			}
		}
	}()

	consulConfig := consulapi.DefaultConfig()
	consulConfig.Address = config.ConsulAddress

	consulClient, err := consulapi.NewClient(consulConfig)
	if err != nil {
		pkg.Log.Error("CRITICAL: Failed to link Consul engine registry", "error", err)
	}

	portInt, err := strconv.Atoi(config.GatewayPort)
	if err != nil {
		pkg.Log.Error("CRITICAL: Invalid gateway port", "error", err)
	}

	hostIp, err := discoverHostIP(config.ConsulAddress)
	if err != nil {
		pkg.Log.Warn("Dynamic host discovery failed, falling back to ServerHost fallback values", "error", err)
		hostIp = config.ServerHost
		if hostIp == "" {
			hostIp = "localhost"
		}
	} else {
		pkg.Log.Info("Programmatically resolved host machine network IP address for Consul lifecycle", "resolved_ip", hostIp)
	}

	registration := &consulapi.AgentServiceRegistration{
		ID:      "eco-platform-api-gateway-1",
		Name:    "eco-platform-api-gateway",
		Tags:    []string{"edge", "routing-proxy", "golang"},
		Port:    portInt,
		Address: hostIp,
		Check: &consulapi.AgentServiceCheck{
			HTTP:     fmt.Sprintf("http://%s:%s/health", hostIp, config.GatewayPort),
			Interval: "10s",
			Timeout:  "5s",
		},
	}

	err = consulClient.Agent().ServiceRegister(registration)
	if err != nil {
		pkg.Log.Warn("Gateway failed to register itself with Consul catalog", "error", err)
	} else {
		pkg.Log.Info("Successfully registered eco-platform-api-gateway with Consul")
	}

	mux := http.NewServeMux()

	pkg.RegisterRoutes(mux, consulClient, config.RouteMappings)
	monitoredMux := pkg.MetricsMiddleware(mux)
	pkg.Log.Info("New way logs, Go Dynamic Edge Gateway running", "port", config.GatewayPort)

	if listenErr := http.ListenAndServe(":"+config.GatewayPort, monitoredMux); listenErr != nil {
		pkg.Log.Error("Fatal server crash during runtime execution", "error", listenErr)
	}
}

func discoverHostIP(consulAddress string) (string, error) {
	targetAddr := consulAddress

	if strings.HasPrefix(targetAddr, "localhost:") || strings.HasPrefix(targetAddr, "127.0.0.1:") {
		targetAddr = "8.8.8.8:80"
	}

	conn, err := net.Dial("udp", targetAddr)
	if err != nil {
		return "", err
	}
	defer func(conn net.Conn) {
		_ = conn.Close()
	}(conn)

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
