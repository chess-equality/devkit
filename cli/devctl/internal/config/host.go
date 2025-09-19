package config

import (
    "os"
    "path/filepath"
    "strings"

    "gopkg.in/yaml.v3"
)

type HostCLIConfig struct {
    DownloadURL string `yaml:"download_url"`
}

type HostConfig struct {
    OverlayPaths []string          `yaml:"overlay_paths"`
    Env          map[string]string `yaml:"env"`
    CLI          HostCLIConfig     `yaml:"cli"`
}

func ReadHostConfig() (HostConfig, string, error) {
    var cfg HostConfig
    cfg.Env = map[string]string{}
    path := strings.TrimSpace(os.Getenv("DEVKIT_CONFIG"))
    if path == "" {
        if dir, err := os.UserConfigDir(); err == nil {
            path = filepath.Join(dir, "devkit", "config.yaml")
        } else if home, err := os.UserHomeDir(); err == nil {
            path = filepath.Join(home, ".config", "devkit", "config.yaml")
        }
    }
    if strings.TrimSpace(path) == "" {
        return cfg, "", nil
    }
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return cfg, filepath.Dir(path), nil
        }
        return cfg, filepath.Dir(path), err
    }
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return cfg, filepath.Dir(path), err
    }
    if cfg.Env == nil {
        cfg.Env = map[string]string{}
    }
    return cfg, filepath.Dir(path), nil
}
