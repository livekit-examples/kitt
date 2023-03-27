package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type LiveKitConfig struct {
	Url       string `yaml:"url"`
	ApiKey    string `yaml:"api_key"`
	SecretKey string `yaml:"secret_key"`
}

type Config struct {
	Development bool          `yaml:"development"`
	LogLevel    string        `yaml:"log_level"`
	LiveKit     LiveKitConfig `yaml:"livekit"`
	Port        int           `yaml:"port"`
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
