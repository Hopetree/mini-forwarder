// Package config handles loading, validating, and managing the YAML configuration
// for TCP port forwarding rules.
package config

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultDialTimeout   = 10 * time.Second
	defaultIdleTimeout   = 300 * time.Second
	defaultMaxRetries    = 3
	defaultRetryInterval = 1 * time.Second
	defaultKeepAlive     = 30 * time.Second
	defaultShutdownTime  = 30 * time.Second
	defaultLogLevel      = "info"
	defaultLogFormat     = "json"
	defaultHotReload     = true
)

// ForwardRule defines a single TCP port forwarding mapping.
type ForwardRule struct {
	Name          string         `mapstructure:"name" yaml:"name"`
	Listen        string         `mapstructure:"listen" yaml:"listen"`
	Target        string         `mapstructure:"target" yaml:"target"`
	DialTimeout   time.Duration  `mapstructure:"dial_timeout" yaml:"dial_timeout"`
	IdleTimeout   *time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout"`
	MaxRetries    int            `mapstructure:"max_retries" yaml:"max_retries"`
	RetryInterval time.Duration  `mapstructure:"retry_interval" yaml:"retry_interval"`
	KeepAlive     time.Duration  `mapstructure:"keep_alive" yaml:"keep_alive"`
}

// GetIdleTimeout returns the idle timeout, applying the default if not set.
func (r ForwardRule) GetIdleTimeout() time.Duration {
	if r.IdleTimeout != nil {
		return *r.IdleTimeout
	}
	return defaultIdleTimeout
}

// Config is the top-level configuration structure.
type Config struct {
	Forwards        []ForwardRule `mapstructure:"forwards" yaml:"forwards"`
	LogLevel        string        `mapstructure:"log_level" yaml:"log_level"`
	LogFormat       string        `mapstructure:"log_format" yaml:"log_format"`
	HotReload       bool          `mapstructure:"hot_reload" yaml:"hot_reload"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" yaml:"shutdown_timeout"`
}

// Load reads and validates the configuration from the given YAML file path.
// It also supports environment variable overrides with the FORWARDER_ prefix.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Global defaults
	v.SetDefault("log_level", defaultLogLevel)
	v.SetDefault("log_format", defaultLogFormat)
	v.SetDefault("hot_reload", defaultHotReload)
	v.SetDefault("shutdown_timeout", defaultShutdownTime.String())

	// Per-rule defaults
	v.SetDefault("forwards.dial_timeout", defaultDialTimeout.String())
	v.SetDefault("forwards.idle_timeout", defaultIdleTimeout.String())
	v.SetDefault("forwards.max_retries", defaultMaxRetries)
	v.SetDefault("forwards.retry_interval", defaultRetryInterval.String())
	v.SetDefault("forwards.keep_alive", defaultKeepAlive.String())

	// Environment variable binding: FORWARDER_LOG_LEVEL, FORWARDER_LOG_FORMAT, etc.
	v.SetEnvPrefix("FORWARDER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	applyRuleDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ptrDuration is a helper to create a *time.Duration.
func ptrDuration(d time.Duration) *time.Duration {
	return &d
}

// applyRuleDefaults fills nil pointer and zero-value fields in each ForwardRule.
func applyRuleDefaults(cfg *Config) {
	for i := range cfg.Forwards {
		r := &cfg.Forwards[i]
		if r.DialTimeout == 0 {
			r.DialTimeout = defaultDialTimeout
		}
		if r.IdleTimeout == nil {
			r.IdleTimeout = ptrDuration(defaultIdleTimeout)
		}
		if r.MaxRetries == 0 {
			r.MaxRetries = defaultMaxRetries
		}
		if r.RetryInterval == 0 {
			r.RetryInterval = defaultRetryInterval
		}
		if r.KeepAlive == 0 {
			r.KeepAlive = defaultKeepAlive
		}
	}
}

// validate checks the configuration for logical errors.
func validate(cfg *Config) error {
	if len(cfg.Forwards) == 0 {
		return fmt.Errorf("config must contain at least one forward rule")
	}

	names := make(map[string]bool)
	listens := make(map[string]bool)

	for i, r := range cfg.Forwards {
		if r.Name == "" {
			return fmt.Errorf("forwards[%d]: name is required", i)
		}
		if names[r.Name] {
			return fmt.Errorf("forwards[%d]: duplicate name %q", i, r.Name)
		}
		names[r.Name] = true

		if r.Listen == "" {
			return fmt.Errorf("forward %q: listen address is required", r.Name)
		}
		if _, err := net.ResolveTCPAddr("tcp", r.Listen); err != nil {
			return fmt.Errorf("forward %q: invalid listen address %q: %w", r.Name, r.Listen, err)
		}
		if listens[r.Listen] {
			return fmt.Errorf("forward %q: duplicate listen address %q", r.Name, r.Listen)
		}
		listens[r.Listen] = true

		if r.Target == "" {
			return fmt.Errorf("forward %q: target address is required", r.Name)
		}
		if _, err := net.ResolveTCPAddr("tcp", r.Target); err != nil {
			return fmt.Errorf("forward %q: invalid target address %q: %w", r.Name, r.Target, err)
		}

		if r.DialTimeout < 0 {
			return fmt.Errorf("forward %q: dial_timeout must be non-negative", r.Name)
		}
		if r.IdleTimeout != nil && *r.IdleTimeout < 0 {
			return fmt.Errorf("forward %q: idle_timeout must be non-negative", r.Name)
		}
		if r.MaxRetries < 0 {
			return fmt.Errorf("forward %q: max_retries must be non-negative", r.Name)
		}
		if r.KeepAlive < 0 {
			return fmt.Errorf("forward %q: keep_alive must be non-negative", r.Name)
		}
	}

	return nil
}
