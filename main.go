package main

import (
	"eco-platform-api-gateway/api"
	"eco-platform-api-gateway/pkg"
	"log"
)

func main() {
	config, err := pkg.LoadConfig()
	pkg.InitStructuredLogger(config)
	if err != nil {
		log.Fatalf("Failed to initialize system parameters: %v", err)
	}

	api.StartGateway(config)
}
