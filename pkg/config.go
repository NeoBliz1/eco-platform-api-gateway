package pkg

import (
	"log"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// LoadConfig loads configuration fields from environment variables
func LoadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		log.Fatalf("No .env file found, relying purely on system environment variables")
	}
	var c Config
	err := envconfig.Process("", &c)
	return &c, err
}
