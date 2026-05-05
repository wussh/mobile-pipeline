package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Project struct {
	Name      string `yaml:"name"`
	RepoURL   string `yaml:"repo_url"`
	LocalPath string `yaml:"local_path"`
}

type Config struct {
	Port            int           `yaml:"port"`
	AuthUser        string        `yaml:"auth_user"`
	AuthPass        string        `yaml:"auth_pass"`
	SecretKey       string        `yaml:"secret_key"`
	LinkExpiry      time.Duration `yaml:"link_expiry"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
	BuildOutputDir  string        `yaml:"build_output_dir"`
	UploadDir       string        `yaml:"upload_dir"`
	Projects        []Project     `yaml:"projects"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.LinkExpiry == 0 {
		cfg.LinkExpiry = 24 * time.Hour
	}
	if cfg.CleanupInterval == 0 {
		cfg.CleanupInterval = 1 * time.Hour
	}
	if cfg.BuildOutputDir == "" {
		cfg.BuildOutputDir = "./builds"
	}
	if cfg.UploadDir == "" {
		cfg.UploadDir = "./uploads"
	}
	return &cfg, nil
}
