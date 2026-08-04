package api

import (
	"eco-platform-api-gateway/pkg"
	"fmt"
	"net/http"
	"os"
	"strconv"

	consulapi "github.com/hashicorp/consul/api"
)

var requestCounter uint64

func StartGateway(config *pkg.Config) {
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

	hostIp := os.Getenv("SERVER_HOST")
	if hostIp == "" {
		hostIp = "localhost"
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

	pkg.RegisterRoutes(mux, consulClient, config.RouteMappings, &requestCounter)

	pkg.Log.Info("New way logs, Go Dynamic Edge Gateway running", "port", config.GatewayPort)

	if listenErr := http.ListenAndServe(":"+config.GatewayPort, mux); listenErr != nil {
		pkg.Log.Error("Fatal server crash during runtime execution", "error", listenErr)
	}
}
