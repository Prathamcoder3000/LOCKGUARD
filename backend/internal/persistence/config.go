package persistence

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds PostgreSQL connection and pool parameters.
type Config struct {
	Host            string        `json:"host"`
	Port            int           `json:"port"`
	Database        string        `json:"database"`
	User            string        `json:"user"`
	Password        string        `json:"-"`
	SSLMode         string        `json:"ssl_mode"`
	MaxOpenConns    int           `json:"max_open_conns"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
	ConnTimeout     time.Duration `json:"conn_timeout"`
}

// LoadConfigFromEnv reads database configuration from environment variables with safe defaults.
// Supports both DATABASE_* and legacy DB_* environment variables.
func LoadConfigFromEnv() (*Config, error) {
	host := getEnvAny([]string{"DATABASE_HOST", "DB_HOST"}, "localhost")
	portStr := getEnvAny([]string{"DATABASE_PORT", "DB_PORT"}, "5432")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid database port: %q", portStr)
	}

	dbName := getEnvAny([]string{"DATABASE_NAME", "DB_NAME"}, "lockguard")
	if strings.TrimSpace(dbName) == "" {
		return nil, fmt.Errorf("database name must not be empty")
	}

	user := getEnvAny([]string{"DATABASE_USER", "DB_USER"}, "lockguard")
	password := getEnvAny([]string{"DATABASE_PASSWORD", "DB_PASSWORD"}, "")
	sslMode := getEnvAny([]string{"DATABASE_SSLMODE", "DB_SSL_MODE"}, "disable")

	maxOpenStr := getEnvAny([]string{"DATABASE_MAX_OPEN_CONNS", "DB_MAX_OPEN_CONNS"}, "20")
	maxOpen, err := strconv.Atoi(maxOpenStr)
	if err != nil || maxOpen <= 0 {
		maxOpen = 20
	}

	maxIdleStr := getEnvAny([]string{"DATABASE_MAX_IDLE_CONNS", "DB_MAX_IDLE_CONNS"}, "10")
	maxIdle, err := strconv.Atoi(maxIdleStr)
	if err != nil || maxIdle < 0 {
		maxIdle = 10
	}

	connMaxLifetime := parseDurationEnv(
		getEnvAny([]string{"DATABASE_CONN_MAX_LIFETIME", "DB_CONN_MAX_LIFETIME_MINUTES"}, "30m"),
		30*time.Minute,
	)

	connTimeout := parseDurationEnv(
		getEnvAny([]string{"DATABASE_CONN_TIMEOUT", "DB_CONN_TIMEOUT"}, "5s"),
		5*time.Second,
	)

	return &Config{
		Host:            host,
		Port:            port,
		Database:        dbName,
		User:            user,
		Password:        password,
		SSLMode:         sslMode,
		MaxOpenConns:    maxOpen,
		MaxIdleConns:    maxIdle,
		ConnMaxLifetime: connMaxLifetime,
		ConnTimeout:     connTimeout,
	}, nil
}

// DSN returns the PostgreSQL connection string for database/sql drivers.
func (c *Config) DSN() string {
	timeoutSeconds := int(c.ConnTimeout.Seconds())
	if timeoutSeconds <= 0 {
		timeoutSeconds = 5
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode, timeoutSeconds,
	)
}

// URL returns a standard PostgreSQL connection URL.
func (c *Config) URL() string {
	var userInfo *url.Userinfo
	if c.Password != "" {
		userInfo = url.UserPassword(c.User, c.Password)
	} else {
		userInfo = url.User(c.User)
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   userInfo,
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Database,
	}
	q := u.Query()
	q.Set("sslmode", c.SSLMode)
	q.Set("connect_timeout", strconv.Itoa(int(c.ConnTimeout.Seconds())))
	u.RawQuery = q.Encode()
	return u.String()
}

// SanitizedDSN returns a connection string with passwords masked for safe logging.
func (c *Config) SanitizedDSN() string {
	maskedPassword := "****"
	if c.Password == "" {
		maskedPassword = "<none>"
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d max_open=%d max_idle=%d",
		c.Host, c.Port, c.User, maskedPassword, c.Database, c.SSLMode, int(c.ConnTimeout.Seconds()),
		c.MaxOpenConns, c.MaxIdleConns,
	)
}

func (c *Config) String() string {
	return c.SanitizedDSN()
}

func getEnvAny(keys []string, fallback string) string {
	for _, k := range keys {
		if val := strings.TrimSpace(os.Getenv(k)); val != "" {
			return val
		}
	}
	return fallback
}

func parseDurationEnv(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	// Support integer minute strings like "30"
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return fallback
}

