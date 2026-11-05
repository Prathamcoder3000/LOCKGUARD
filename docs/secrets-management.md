# LockGuard Secrets & Configuration Management Specification

**Document Version:** 1.0.0  
**Phase:** 5 (Security Hardening, Performance Optimization & Production Readiness)  
**Classification:** Operational Security & Secrets Management  
**System Title:** Fault Tolerant Distributed Lock Service (LOCKGUARD)

---

## 1. Overview & Threat Vectors

Improper secrets management in distributed infrastructure frequently leads to:
1. Hardcoded private keys and default database passwords committed to version control.
2. Accidental credential leaks into operational logging pipelines (`stdout`, cloud logs, APM).
3. Production systems operating with default development secrets vulnerable to dictionary attacks.

LockGuard addresses these risks through strict validation, automatic redaction via Go's `fmt.Stringer` interface, and clear separation of configuration templates.

---

## 2. Secrets Redaction & Logging Safety

To prevent accidental credential leaks into logs or monitoring dashboards, all configuration structures containing sensitive fields implement the standard Go `fmt.Stringer` interface:

### 2.1 Implemented Redaction Methods (`backend/internal/config/config.go`):

```go
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
```

Whenever loggers format configuration objects with `%v` or `%s`, sensitive credentials (`JWTSecretKey`, `APITokenSalt`, `DB.Password`) are masked as `[REDACTED]`.

---

## 3. Production Environment Validation Rules

When `cfg.Environment == "production"`, `Config.Validate()` enforces strict entropy and security assertions:

1. **JWT Secret Key Strength:**
   - Must NOT be the default development secret (`lockguard_default_secret_key_change_in_production_min32`).
   - Must be at least 32 characters in length ($\ge 256$ bits of entropy).
2. **Database Password Hardening:**
   - Must NOT be empty, `"lockguard_password"`, or `"password"`.
3. **Database SSL Mode:**
   - Must NOT be `"disable"`. Production deployments must specify `"require"`, `"verify-ca"`, or `"verify-full"`.

If any of these conditions fail, server initialization aborts immediately with a descriptive fatal error.

---

## 4. Git Invariant & Template Configuration

- `.env.example` in the project root provides documented configuration templates with placeholder values.
- `.gitignore` explicitly ignores `.env`, `.env.local`, `.env.*.local`, `*.pem`, `*.key`, and `*.crt`.
- No sensitive credentials, private keys, or passwords are committed to Git history.

