package pkg

import (
	consulapi "github.com/hashicorp/consul/api"
	"gopkg.in/yaml.v3"
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
	// Extract the CONSUL_ADDRESS value from the environment
	if err := envconfig.Process("", &c); err != nil {
		log.Printf("[ERROR] Failed to extract system bootstrap tags: %v", err)
	}

	// Connect to Consul using the bootstrap address
	consulConfig := consulapi.DefaultConfig()
	if c.ConsulAddress != "" {
		consulConfig.Address = c.ConsulAddress
	}

	client, err := consulapi.NewClient(consulConfig)
	if err != nil {
		log.Printf("[ERROR] Failed to initialize Consul connection client: %v", err)
		return &c, err
	}

	// Fetch the single flat YAML block from your custom Consul path
	kv := client.KV()
	targetKey := "config/eco-monitoring-gateway/data"

	log.Printf("[INFO] Fetching flat YAML configuration from Consul key '%s' at address %s...", targetKey, consulConfig.Address)

	pair, _, err := kv.Get(targetKey, nil)
	if err != nil {
		log.Printf("[ERROR] Consul KV lookup error for key '%s': %v", targetKey, err)
		return &c, err
	}

	if pair == nil || len(pair.Value) == 0 {
		log.Printf("[ERROR] Consul key '%s' is empty or missing", targetKey)
		return &c, nil
	}

	// Parse the raw YAML directly into the root level fields of your struct
	if err := yaml.Unmarshal(pair.Value, &c); err != nil {
		log.Printf("[ERROR] Failed to parse flat YAML data from Consul: %v", err)
		return &c, err
	}

	log.Println("[INFO] Successfully hydrated settings from flat Consul configuration block")
	return &c, err
}
