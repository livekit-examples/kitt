package config

import (
	"fmt"

	"github.com/livekit/protocol/logger"
	"gopkg.in/yaml.v3"
)

type LiveKitConfig struct {
	Url       string `yaml:"url"`
	ApiKey    string `yaml:"api_key"`
	SecretKey string `yaml:"secret_key"`
}

type Config struct {
	Logger  logger.Config `yaml:"logging"`
	LiveKit LiveKitConfig `yaml:"livekit"`
	Port    int           `yaml:"port"`
}

func NewConfig(content string) (*Config, error) {
	conf := &Config{}

	if content != "" {
		if err := yaml.Unmarshal([]byte(content), conf); err != nil {
			return nil, fmt.Errorf("could not parse config: %v", err)
		}
	}

	return conf, nil
}
