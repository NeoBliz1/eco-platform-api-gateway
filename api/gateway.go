package api

import (
	"eco-platform-api-gateway/pkg"
	"fmt"
	consulapi "github.com/hashicorp/consul/api"
	"log"
	"net/http"
	"os"
	"strconv"
)

var requestCounter uint64

func StartGateway(config *pkg.Config) {
	consulConfig := consulapi.DefaultConfig()
	consulConfig.Address = config.ConsulAddress

	consulClient, err := consulapi.NewClient(consulConfig)
	if err != nil {
		log.Fatalf("CRITICAL: Failed to link Consul engine registry: %v", err)
	}

	portInt, err := strconv.Atoi(config.GatewayPort)
	if err != nil {
		log.Fatalf("CRITICAL: Invalid gateway port: %v", err)
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
		log.Printf("WARN: Gateway failed to register itself with Consul catalog: %v", err)
	} else {
		log.Println("INFO: Successfully registered eco-platform-api-gateway with Consul!")
	}

	mux := http.NewServeMux()

	pkg.RegisterRoutes(mux, consulClient, config.RouteMappings, &requestCounter)

	log.Printf("INFO: Go Dynamic Edge Gateway running on port %s", config.GatewayPort)
	log.Fatal(http.ListenAndServe(":"+config.GatewayPort, mux))
}
