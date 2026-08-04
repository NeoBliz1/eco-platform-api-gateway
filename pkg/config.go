package pkg

import (
	"log/slog"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration properties for application runtime context
type Config struct {
	GatewayPort   string `envconfig:"GATEWAY_PORT"`
	ConsulAddress string `envconfig:"CONSUL_ADDRESS"`
	RouteMappings string `envconfig:"ROUTE_MAPPINGS"`
}

// LoadConfig loads configuration fields from environment variables
func LoadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found, relying purely on system environment variables")
	}
	var c Config
	err := envconfig.Process("", &c)
	return &c, err
}
