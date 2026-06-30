package pkg

import (
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"log"
)

// Config holds all the configuration for the application.
type Config struct {
	GatewayPort   string `envconfig:"GATEWAY_PORT"`
	ConsulAddress string `envconfig:"CONSUL_ADDRESS"`
	RouteMappings string `envconfig:"ROUTE_MAPPINGS"`
}

// LoadConfig loads the configuration from environment variables.
func LoadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying purely on system environment variables")
	}
	var c Config
	err := envconfig.Process("", &c)
	return &c, err
}
