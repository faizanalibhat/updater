// Package config holds the persistent on-disk configuration for the agent.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"snapsec-agent/internal/dotenv"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config is the persistent on-disk configuration for the agent.
type Config struct {
	// AgentID is assigned by the admin server on first registration.
	AgentID string `yaml:"agent_id"`

	// AdminURL is the base URL of the admin control plane,
	// e.g. https://admin.snapsec.co
	AdminURL string `yaml:"admin_url"`

	// BaseURL is the public-facing URL where this on-prem instance is
	// hosted (matches BASE_URL in the product .env). Reported to the
	// admin server on registration.
	BaseURL string `yaml:"base_url"`

	// AdminBasePath is the URL prefix under AdminURL where the agent /
	// instance routes are mounted. The default matches the obfuscated
	// prefix used by admin.snapsec.co; override per environment.
	AdminBasePath string `yaml:"admin_base_path"`

	// EnrollmentToken is an optional one-time token used during initial
	// registration. It is cleared from the file once an agent_id is issued.
	EnrollmentToken string `yaml:"enrollment_token,omitempty"`

	// CFAccessClientID and CFAccessClientSecret authenticate the agent to
	// Cloudflare Access (service token) when the admin host sits behind
	// Cloudflare Zero Trust. Both are optional; when set they are sent as
	// CF-Access-Client-Id / CF-Access-Client-Secret on every request.
	CFAccessClientID     string `yaml:"cf_access_client_id,omitempty"`
	CFAccessClientSecret string `yaml:"cf_access_client_secret,omitempty"`

	// HeartbeatIntervalSeconds controls how often the agent beats.
	HeartbeatIntervalSeconds int `yaml:"heartbeat_interval_seconds"`

	// InstallDir is the directory containing setup.sh (used by the
	// update_application capability).
	InstallDir string `yaml:"install_dir"`

	// MongoURI is the connection string used by the set_license_expiry
	// capability to talk to the local mongo instance. Treated as a
	// fallback only — the live values are derived from
	// <InstallDir>/.env (MONGODB_HOST/PORT/USER/PASS) on every access
	// via MongoConnection().
	MongoURI string `yaml:"mongo_uri,omitempty"`

	// MongoDatabase is the database that holds the orgs collection.
	// Same fallback semantics as MongoURI: read from .env on every
	// access via MongoConnection().
	MongoDatabase string `yaml:"mongo_database,omitempty"`

	// CurrentVersion tracks the version of the running agent binary.
	CurrentVersion string `yaml:"current_version"`

	// path is where the file lives on disk (not serialized).
	path string `yaml:"-"`
	mu   sync.Mutex
}

// DefaultPath returns the default config path: /etc/snapsec-agent/config.yaml,
// or $SNAPSEC_AGENT_CONFIG when set.
func DefaultPath() string {
	if p := os.Getenv("SNAPSEC_AGENT_CONFIG"); p != "" {
		return p
	}
	return "/etc/snapsec-agent/config.yaml"
}

// Load reads the config from disk. If the file does not exist, a Config
// with sane defaults is returned (and path set), but no file is created.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}

	cfg := &Config{
		path:                     path,
		AdminURL:                 "https://admin.snapsec.co",
		AdminBasePath:            "/z4to1w2Ww0tviBr5fAMusiSLHsUKf2GKP3cz4xdTt6fWT05X/v1/instances",
		HeartbeatIntervalSeconds: 30,
		InstallDir:               "/root/staging",
		MongoDatabase:            "auth",
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.path = path

	if cfg.HeartbeatIntervalSeconds <= 0 {
		cfg.HeartbeatIntervalSeconds = 30
	}
	if cfg.AdminURL == "" {
		cfg.AdminURL = "https://admin.snapsec.co"
	}
	if cfg.AdminBasePath == "" {
		cfg.AdminBasePath = "/z4to1w2Ww0tviBr5fAMusiSLHsUKf2GKP3cz4xdTt6fWT05X/v1/instances"
	}
	return cfg, nil
}

// Save writes the config back to disk atomically.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.path == "" {
		c.path = DefaultPath()
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp config: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("rename tmp config: %w", err)
	}
	return nil
}

// Path returns the file path this config was loaded from.
func (c *Config) Path() string { return c.path }

// SetAgentID sets the agent id and persists the config.
func (c *Config) SetAgentID(id string) error {
	c.AgentID = id
	c.EnrollmentToken = "" // one-time use
	return c.Save()
}

// SetVersion updates the current version and persists the config.
func (c *Config) SetVersion(v string) error {
	c.CurrentVersion = v
	return c.Save()
}

// MongoConnection resolves the mongo URI and database name to use right
// now. It reads <InstallDir>/.env each call (so the agent always tracks
// the live MONGODB_HOST/PORT/USER/PASS values managed by setup.sh) and
// falls back to the persisted Config fields when the .env is missing or
// incomplete. The database defaults to "snapsec" when neither source
// supplies one.
func (c *Config) MongoConnection() (uri, db string) {
	if c.InstallDir != "" {
		env, err := dotenv.Read(filepath.Join(c.InstallDir, ".env"))
		if err == nil {
			// Host-side overrides: the agent runs outside docker, so the
			// product .env's MONGODB_HOST=mongodb (a docker-network DNS
			// name) is unreachable. Allow the systemd unit (or operator)
			// to redirect to a host-reachable address without touching
			// the shared .env that compose services depend on.
			if v := os.Getenv("SNAPSEC_AGENT_MONGODB_HOST"); v != "" {
				env["MONGODB_HOST"] = v
			}
			if v := os.Getenv("SNAPSEC_AGENT_MONGODB_PORT"); v != "" {
				env["MONGODB_PORT"] = v
			}
			if u, d := dotenv.MongoURIFromEnv(env); u != "" {
				if d == "" {
					d = c.MongoDatabase
				}
				if d == "" {
					d = "auth"
				}
				return u, d
			}
		}
	}
	uri = c.MongoURI
	db = c.MongoDatabase
	if db == "" {
		db = "auth"
	}
	return uri, db
}
