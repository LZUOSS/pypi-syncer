package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for pypi-mirror.
type Config struct {
	Listen   string `yaml:"listen"`
	RepoPath string `yaml:"repo_path"`
	Prefix   string `yaml:"prefix"`

	Upstream UpstreamConfig `yaml:"upstream"`
	TLS      TLSConfig      `yaml:"tls"`

	TrustedProxies []string `yaml:"trusted_proxies"`

	IPModes IPModesConfig `yaml:"ip_modes"`

	Cache CacheConfig `yaml:"cache"`
	Sync  SyncConfig  `yaml:"sync"`
	Log   LogConfig   `yaml:"log"`

	Timeouts TimeoutsConfig `yaml:"timeouts"`
}

type UpstreamConfig struct {
	PypiURL     string `yaml:"pypi_url"`
	PackagesURL string `yaml:"packages_url"`
}

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type IPModesConfig struct {
	Default string       `yaml:"default"`
	Rules   []IPModeRule `yaml:"rules"`
}

type IPModeRule struct {
	CIDR string `yaml:"cidr"`
	Mode string `yaml:"mode"`
}

type CacheConfig struct {
	SizeLimit      HumanSize `yaml:"size_limit"`
	FilesizeLimit  HumanSize `yaml:"filesize_limit"`
	MinVoteCount   int       `yaml:"min_vote_count"`
	VoteWindow     string    `yaml:"vote_window"`
	DedupWindow    string    `yaml:"dedup_window"`
	SizeDBTTL      string    `yaml:"size_db_ttl"`
}

type SyncConfig struct {
	Retry                   int    `yaml:"retry"`
	DownloadErrorThreshold  int    `yaml:"download_error_threshold"`
	UserAgent               string `yaml:"user_agent"`
	ConcurrentDownloads     int    `yaml:"concurrent_downloads"`
}

type LogConfig struct {
	Path   string `yaml:"path"`
	Format string `yaml:"format"`
}

type TimeoutsConfig struct {
	Read     string `yaml:"read"`
	Write    string `yaml:"write"`
	Idle     string `yaml:"idle"`
	Upstream string `yaml:"upstream"`
}

// Load reads and parses a config file from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Prefix == "" {
		c.Prefix = "/pypi"
	}
	if c.Upstream.PypiURL == "" {
		c.Upstream.PypiURL = "https://pypi.org"
	}
	if c.IPModes.Default == "" {
		c.IPModes.Default = "302"
	}
	if c.Cache.MinVoteCount == 0 {
		c.Cache.MinVoteCount = 2
	}
	if c.Cache.VoteWindow == "" {
		c.Cache.VoteWindow = "7d"
	}
	if c.Cache.DedupWindow == "" {
		c.Cache.DedupWindow = "5m"
	}
	if c.Cache.SizeDBTTL == "" {
		c.Cache.SizeDBTTL = "2d"
	}
	if c.Sync.Retry == 0 {
		c.Sync.Retry = 3
	}
	if c.Sync.DownloadErrorThreshold == 0 {
		c.Sync.DownloadErrorThreshold = 5
	}
	if c.Sync.UserAgent == "" {
		c.Sync.UserAgent = "pypi-mirror/1.0"
	}
	if c.Sync.ConcurrentDownloads == 0 {
		c.Sync.ConcurrentDownloads = 4
	}
	if c.Log.Format == "" {
		c.Log.Format = "mirror-json"
	}
	if c.Timeouts.Read == "" {
		c.Timeouts.Read = "30s"
	}
	if c.Timeouts.Write == "" {
		c.Timeouts.Write = "120s"
	}
	if c.Timeouts.Idle == "" {
		c.Timeouts.Idle = "60s"
	}
	if c.Timeouts.Upstream == "" {
		c.Timeouts.Upstream = "60s"
	}
}
