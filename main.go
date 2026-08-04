package main

import (
	"eco-platform-api-gateway/api"
	"eco-platform-api-gateway/pkg"
	"log"
)

func main() {
	pkg.InitStructuredLogger()
	config, err := pkg.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to initialize system parameters: %v", err)
	}

	api.StartGateway(config)
}
