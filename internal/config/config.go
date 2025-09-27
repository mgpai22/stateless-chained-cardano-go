package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Submit    SubmitConfig    `yaml:"submit"`
	TxBuilder TxBuilderConfig `yaml:"txbuilder"`
	Wallet    WalletConfig    `yaml:"wallet"`
	Network   string          `yaml:"network"   envconfig:"NETWORK"`
}

type SubmitConfig struct {
	Address    string `yaml:"address"     envconfig:"SUBMIT_TCP_ADDRESS"`
	SocketPath string `yaml:"socket_path" envconfig:"SUBMIT_SOCKET_PATH"`
	Url        string `yaml:"url"         envconfig:"SUBMIT_URL"`
}

type TxBuilderConfig struct {
	BlockfrostApiKey  string `yaml:"blockfrost_api_key"  envconfig:"BLOCKFROST_API_KEY"`
	KupoUrl           string `yaml:"kupo_url"            envconfig:"KUPO_URL"`
	CardanoMonitorUrl string `yaml:"cardano_monitor_url" envconfig:"CARDANO_MONITOR_URL"`
}

type WalletConfig struct {
	Mnemonic string `yaml:"mnemonic" envconfig:"MNEMONIC"`
}

var globalConfig = &Config{
	Network: "mainnet",
}

// LoadFromYAML loads configuration from a YAML file
func LoadFromYAML(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to read config file %s: %w",
			configPath,
			err,
		)
	}

	config := &Config{
		Network: "mainnet", // Set default
	}

	err = yaml.Unmarshal(data, config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	return config, nil
}

// LoadWithConfigFile loads configuration from YAML file with environment variable override
func LoadWithConfigFile(configPath string) (*Config, error) {
	// First load from YAML file
	config, err := LoadFromYAML(configPath)
	if err != nil {
		return nil, err
	}

	// Set the global config to the loaded values
	globalConfig = config

	// Load any .env file
	err = godotenv.Load()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Override with environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err = envconfig.Process("dummy", globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %w", err)
	}

	return globalConfig, nil
}

func Load() (*Config, error) {
	// Try to load from config.yaml if it exists
	if _, err := os.Stat("config.yaml"); err == nil {
		return LoadWithConfigFile("config.yaml")
	}

	// Fallback to environment variables only
	// Load any .env file
	err := godotenv.Load()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err = envconfig.Process("dummy", globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %w", err)
	}
	return globalConfig, nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}
