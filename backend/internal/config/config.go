package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config encapsulates all runtime configuration for LockGuard services.
type Config struct {
	Environment string
	AppName     string
	LogLevel    string
	LogFormat   string

	HTTP HTTPConfig
	DB   DBConfig
	Auth AuthConfig
	Raft RaftConfig

	Storage StorageConfig
	Lease   LeaseConfig
	Metrics MetricsConfig

	Gateway   GatewayConfig
	Heartbeat HeartbeatConfig
	Renewal   RenewalConfig
}

// GatewayConfig defines reverse-proxy and leader-discovery settings.
type GatewayConfig struct {
	CoordinatorEndpoints []string
	LeaderCacheTTL       time.Duration
	MaxRedirectAttempts  int
	RequestTimeout       time.Duration
}

// HeartbeatConfig defines client heartbeat session management settings.
type HeartbeatConfig struct {
	Interval        time.Duration
	Timeout         time.Duration
	CleanupInterval time.Duration
}

// RenewalConfig defines automatic lease renewal parameters.
type RenewalConfig struct {
	SafetyMargin time.Duration
	RetryInitial time.Duration
	RetryMax     time.Duration
	MaxAttempts  int
}

// HTTPConfig defines the HTTP server parameters.
type HTTPConfig struct {
	Port            int
	Host            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// DBConfig defines PostgreSQL connection parameters.
// Note: Authoritative lock state is NOT stored in PostgreSQL.
type DBConfig struct {
	Host                   string
	Port                   int
	User                   string
	Password               string
	DBName                 string
	SSLMode                string
	MaxOpenConns           int
	MaxIdleConns           int
	ConnMaxLifetimeMinutes time.Duration
}

// AuthConfig defines authentication parameters.
type AuthConfig struct {
	Enabled        bool
	JWTSecretKey   string
	JWTExpirationH int
	APITokenSalt   string
}

// RaftConfig defines Raft consensus parameters.
type RaftConfig struct {
	NodeID              string
	ClusterNodes        []string
	BindAddr            string
	AdvertiseAddr       string
	HeartbeatTimeout    time.Duration
	ElectionTimeout     time.Duration
	CommitTimeout       time.Duration
	SnapshotIntervalSec time.Duration
	SnapshotThreshold   uint64
	CoordinatorAPIPort  int
	CoordinatorAPIHost  string
	EnableTLS           bool
	TLSCertFile         string
	TLSKeyFile          string
	TLSCAFile           string
}

// StorageConfig defines file paths for durable persistence.
type StorageConfig struct {
	DataDir               string
	RaftLogStorePath      string
	RaftStableStorePath   string
	RaftSnapshotStorePath string
}

// LeaseConfig defines default and boundary lease durations.
type LeaseConfig struct {
	DefaultTTLSec       int
	MaxTTLSec           int
	MinTTLSec           int
	FencingTokenInitial uint64
}

// MetricsConfig defines Prometheus metrics parameters.
type MetricsConfig struct {
	Enabled bool
	Port    int
}

// LoadConfig loads configuration from environment variables with safe defaults.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Environment: getEnv("ENVIRONMENT", "development"),
		AppName:     getEnv("APP_NAME", "LockGuard"),
		LogLevel:    strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:   strings.ToLower(getEnv("LOG_FORMAT", "json")),

		HTTP: HTTPConfig{
			Port:            getEnvInt("HTTP_PORT", 8080),
			Host:            getEnv("HTTP_HOST", "0.0.0.0"),
			ReadTimeout:     time.Duration(getEnvInt("HTTP_READ_TIMEOUT_SECONDS", 15)) * time.Second,
			WriteTimeout:    time.Duration(getEnvInt("HTTP_WRITE_TIMEOUT_SECONDS", 15)) * time.Second,
			IdleTimeout:     time.Duration(getEnvInt("HTTP_IDLE_TIMEOUT_SECONDS", 60)) * time.Second,
			ShutdownTimeout: time.Duration(getEnvInt("HTTP_SHUTDOWN_TIMEOUT_SECONDS", 10)) * time.Second,
		},

		DB: DBConfig{
			Host:                   getEnv("DB_HOST", "localhost"),
			Port:                   getEnvInt("DB_PORT", 5432),
			User:                   getEnv("DB_USER", "lockguard_user"),
			Password:               getEnv("DB_PASSWORD", "lockguard_password"),
			DBName:                 getEnv("DB_NAME", "lockguard_db"),
			SSLMode:                getEnv("DB_SSL_MODE", "disable"),
			MaxOpenConns:           getEnvInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:           getEnvInt("DB_MAX_IDLE_CONNS", 10),
			ConnMaxLifetimeMinutes: time.Duration(getEnvInt("DB_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute,
		},

		Auth: AuthConfig{
			Enabled:        getEnvBool("AUTH_ENABLED", true),
			JWTSecretKey:   getEnv("JWT_SECRET_KEY", "lockguard_default_secret_key_change_in_production_min32"),
			JWTExpirationH: getEnvInt("JWT_EXPIRATION_HOURS", 24),
			APITokenSalt:   getEnv("API_TOKEN_SALT", "lockguard_default_salt"),
		},

		Raft: RaftConfig{
			NodeID:              getEnv("NODE_ID", "coordinator-a"),
			ClusterNodes:        parseCommaSeparated(getEnv("RAFT_CLUSTER_NODES", "coordinator-a:7000,coordinator-b:7000,coordinator-c:7000")),
			BindAddr:            getEnv("RAFT_BIND_ADDR", "0.0.0.0:7000"),
			AdvertiseAddr:       getEnv("RAFT_ADVERTISE_ADDR", "coordinator-a:7000"),
			HeartbeatTimeout:    time.Duration(getEnvInt("RAFT_HEARTBEAT_TIMEOUT_MS", 1000)) * time.Millisecond,
			ElectionTimeout:     time.Duration(getEnvInt("RAFT_ELECTION_TIMEOUT_MS", 1000)) * time.Millisecond,
			CommitTimeout:       time.Duration(getEnvInt("RAFT_COMMIT_TIMEOUT_MS", 50)) * time.Millisecond,
			SnapshotIntervalSec: time.Duration(getEnvInt("RAFT_SNAPSHOT_INTERVAL_SECONDS", 120)) * time.Second,
			SnapshotThreshold:   uint64(getEnvInt("RAFT_SNAPSHOT_THRESHOLD", 8192)),
			CoordinatorAPIPort:  getEnvInt("COORDINATOR_API_PORT", 8001),
			CoordinatorAPIHost:  getEnv("COORDINATOR_API_HOST", "0.0.0.0"),
			EnableTLS:           getEnvBool("RAFT_ENABLE_TLS", false),
			TLSCertFile:         getEnv("RAFT_TLS_CERT_FILE", ""),
			TLSKeyFile:          getEnv("RAFT_TLS_KEY_FILE", ""),
			TLSCAFile:           getEnv("RAFT_TLS_CA_FILE", ""),
		},

		Storage: StorageConfig{
			DataDir:               getEnv("DATA_DIR", "./data"),
			RaftLogStorePath:      getEnv("RAFT_LOG_STORE_PATH", "./data/raft.db"),
			RaftStableStorePath:   getEnv("RAFT_STABLE_STORE_PATH", "./data/stable.db"),
			RaftSnapshotStorePath: getEnv("RAFT_SNAPSHOT_STORE_PATH", "./data/snapshots"),
		},

		Lease: LeaseConfig{
			DefaultTTLSec:       getEnvInt("DEFAULT_LEASE_TTL_SECONDS", 10),
			MaxTTLSec:           getEnvInt("MAX_LEASE_TTL_SECONDS", 300),
			MinTTLSec:           getEnvInt("MIN_LEASE_TTL_SECONDS", 1),
			FencingTokenInitial: uint64(getEnvInt("FENCING_TOKEN_INITIAL", 1000)),
		},

		Metrics: MetricsConfig{
			Enabled: getEnvBool("METRICS_ENABLED", true),
			Port:    getEnvInt("METRICS_PORT", 9090),
		},

		Gateway: GatewayConfig{
			CoordinatorEndpoints: parseCommaSeparated(getEnv("COORDINATOR_ENDPOINTS", "http://127.0.0.1:8001")),
			LeaderCacheTTL:       time.Duration(getEnvInt("GATEWAY_LEADER_CACHE_TTL_MS", 5000)) * time.Millisecond,
			MaxRedirectAttempts:  getEnvInt("GATEWAY_MAX_REDIRECT_ATTEMPTS", 2),
			RequestTimeout:       time.Duration(getEnvInt("GATEWAY_REQUEST_TIMEOUT_MS", 5000)) * time.Millisecond,
		},

		Heartbeat: HeartbeatConfig{
			Interval:        time.Duration(getEnvInt("HEARTBEAT_INTERVAL_MS", 3000)) * time.Millisecond,
			Timeout:         time.Duration(getEnvInt("HEARTBEAT_TIMEOUT_MS", 10000)) * time.Millisecond,
			CleanupInterval: time.Duration(getEnvInt("HEARTBEAT_CLEANUP_INTERVAL_MS", 2000)) * time.Millisecond,
		},

		Renewal: RenewalConfig{
			SafetyMargin: time.Duration(getEnvInt("RENEWAL_SAFETY_MARGIN_MS", 2000)) * time.Millisecond,
			RetryInitial: time.Duration(getEnvInt("RENEWAL_RETRY_INITIAL_MS", 100)) * time.Millisecond,
			RetryMax:     time.Duration(getEnvInt("RENEWAL_RETRY_MAX_MS", 2000)) * time.Millisecond,
			MaxAttempts:  getEnvInt("RENEWAL_MAX_ATTEMPTS", 3),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// Validate checks that all required configuration values are within acceptable bounds.
func (c *Config) Validate() error {
	var errs []string

	if c.HTTP.Port <= 0 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Sprintf("invalid HTTP port: %d (must be 1-65535)", c.HTTP.Port))
	}

	if c.DB.Port <= 0 || c.DB.Port > 65535 {
		errs = append(errs, fmt.Sprintf("invalid DB port: %d (must be 1-65535)", c.DB.Port))
	}

	if c.Raft.NodeID == "" {
		errs = append(errs, "Raft NodeID cannot be empty")
	}

	if len(c.Raft.ClusterNodes) == 0 {
		errs = append(errs, "Raft ClusterNodes must specify at least one peer")
	}

	if c.Lease.MinTTLSec <= 0 {
		errs = append(errs, "Min lease TTL must be greater than 0")
	}

	if c.Lease.MaxTTLSec < c.Lease.MinTTLSec {
		errs = append(errs, "Max lease TTL must be greater than or equal to Min lease TTL")
	}

	if c.Lease.DefaultTTLSec < c.Lease.MinTTLSec || c.Lease.DefaultTTLSec > c.Lease.MaxTTLSec {
		errs = append(errs, "Default lease TTL must be between MinTTL and MaxTTL")
	}

	if c.HTTP.ShutdownTimeout <= 0 {
		errs = append(errs, "HTTP shutdown timeout must be positive")
	}

	if c.Gateway.MaxRedirectAttempts < 0 {
		errs = append(errs, "Gateway max redirect attempts cannot be negative")
	}

	if c.Heartbeat.Timeout > 0 && c.Heartbeat.Interval > 0 && c.Heartbeat.Interval >= c.Heartbeat.Timeout {
		errs = append(errs, "Heartbeat interval must be strictly less than heartbeat timeout")
	}

	if c.Renewal.MaxAttempts < 0 {
		errs = append(errs, "Renewal max attempts cannot be negative")
	}

	if c.Environment == "production" {
		if c.Auth.JWTSecretKey == "lockguard_default_secret_key_change_in_production_min32" {
			errs = append(errs, "production environment requires a secure, non-default JWT_SECRET_KEY")
		}
		if len(c.Auth.JWTSecretKey) < 32 {
			errs = append(errs, "JWT_SECRET_KEY must be at least 32 characters in production")
		}
		if c.DB.Password == "" || c.DB.Password == "lockguard_password" || c.DB.Password == "password" {
			errs = append(errs, "production environment requires a secure, non-default DB_PASSWORD")
		}
		if c.DB.SSLMode == "disable" {
			errs = append(errs, "production environment requires secure DB_SSL_MODE (require, verify-ca, or verify-full)")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}

	return nil
}

// String implements fmt.Stringer for Config, ensuring sensitive credentials are never logged in plaintext.
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{Environment:%s, AppName:%s, HTTP:%+v, DB:%s, Auth:%s, Raft:%+v, Storage:%+v, Lease:%+v, Gateway:%+v, Heartbeat:%+v, Renewal:%+v}",
		c.Environment, c.AppName, c.HTTP, c.DB.String(), c.Auth.String(), c.Raft, c.Storage, c.Lease, c.Gateway, c.Heartbeat, c.Renewal,
	)
}

// String implements fmt.Stringer for DBConfig, redacting Password.
func (db DBConfig) String() string {
	return fmt.Sprintf(
		"DBConfig{Host:%s, Port:%d, User:%s, Password:[REDACTED], DBName:%s, SSLMode:%s, MaxOpenConns:%d, MaxIdleConns:%d}",
		db.Host, db.Port, db.User, db.DBName, db.SSLMode, db.MaxOpenConns, db.MaxIdleConns,
	)
}

// String implements fmt.Stringer for AuthConfig, redacting JWTSecretKey and APITokenSalt.
func (a AuthConfig) String() string {
	return fmt.Sprintf(
		"AuthConfig{Enabled:%t, JWTSecretKey:[REDACTED], JWTExpirationH:%d, APITokenSalt:[REDACTED]}",
		a.Enabled, a.JWTExpirationH,
	)
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if valStr, ok := os.LookupEnv(key); ok && strings.TrimSpace(valStr) != "" {
		if val, err := strconv.Atoi(strings.TrimSpace(valStr)); err == nil {
			return val
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if valStr, ok := os.LookupEnv(key); ok && strings.TrimSpace(valStr) != "" {
		lower := strings.ToLower(strings.TrimSpace(valStr))
		if lower == "true" || lower == "1" || lower == "yes" {
			return true
		}
		if lower == "false" || lower == "0" || lower == "no" {
			return false
		}
	}
	return defaultVal
}

func parseCommaSeparated(val string) []string {
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
