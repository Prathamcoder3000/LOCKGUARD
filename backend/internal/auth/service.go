package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Service provides identity authentication, token issuance, and account lifecycle.
type Service struct {
	mu           sync.RWMutex
	secretKey    string
	tokenTTL     time.Duration
	users        map[string]*UserAccount // username -> account
	adminToken   string
}

// NewService creates and initializes an AuthService with standard administrative and client accounts.
func NewService(secretKey string, tokenTTL time.Duration, adminToken string) *Service {
	if tokenTTL <= 0 {
		tokenTTL = 24 * time.Hour
	}

	s := &Service{
		secretKey:  secretKey,
		tokenTTL:   tokenTTL,
		users:      make(map[string]*UserAccount),
		adminToken: adminToken,
	}

	// Seed standard development / operational user accounts with secure random salts
	s.seedDefaultAccounts()
	return s
}

func (s *Service) seedDefaultAccounts() {
	// 1. Cluster Administrator (ADMIN)
	s.registerInternal("admin", "admin_secure_pass_phase5", RoleAdmin, "admin-system")

	// 2. Primary Worker Client (CLIENT) with bound OwnerID "worker-1"
	s.registerInternal("client_worker", "worker_secure_pass_phase5", RoleClient, "worker-1")

	// 3. Secondary Worker Client (CLIENT) with bound OwnerID "worker-2"
	s.registerInternal("client_worker_2", "worker_secure_pass_phase5", RoleClient, "worker-2")

	// 4. Audit & Compliance Officer (READ_ONLY)
	s.registerInternal("auditor", "auditor_secure_pass_phase5", RoleReadOnly, "auditor-01")
}

func (s *Service) registerInternal(username, password, role, ownerID string) {
	salt := generateSalt(16)
	hash := hashPassword(password, salt)

	s.users[username] = &UserAccount{
		ID:           fmt.Sprintf("usr-%s", username),
		Username:     username,
		PasswordHash: hash,
		Salt:         salt,
		Role:         role,
		OwnerID:      ownerID,
		CreatedAt:    time.Now().UTC(),
	}
}

// Authenticate verifies credentials and returns a signed JWT.
func (s *Service) Authenticate(username, password string) (*LoginResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[username]
	if !exists {
		return nil, ErrInvalidCredentials
	}

	expectedHash := hashPassword(password, user.Salt)
	if subtle.ConstantTimeCompare([]byte(user.PasswordHash), []byte(expectedHash)) != 1 {
		return nil, ErrInvalidCredentials
	}

	now := time.Now().UTC()
	exp := now.Add(s.tokenTTL)

	// Subject represents the authenticated owner ID for clients, or user ID for admins
	sub := user.OwnerID
	if sub == "" {
		sub = user.ID
	}

	claims := Claims{
		Subject:   sub,
		Username:  user.Username,
		Role:      user.Role,
		Issuer:    "lockguard",
		Audience:  "lockguard-api",
		IssuedAt:  now.Unix(),
		ExpiresAt: exp.Unix(),
	}

	token, err := GenerateJWT(claims, s.secretKey)
	if err != nil {
		return nil, fmt.Errorf("failed to issue jwt: %w", err)
	}

	return &LoginResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresIn: int64(s.tokenTTL.Seconds()),
		User: UserBrief{
			ID:       user.ID,
			Username: user.Username,
			Role:     user.Role,
			OwnerID:  user.OwnerID,
		},
	}, nil
}

// ValidateToken parses and cryptographically verifies a JWT or static admin bearer token.
func (s *Service) ValidateToken(tokenString string) (*Claims, error) {
	// Fallback check: if token matches static adminToken
	if s.adminToken != "" && subtle.ConstantTimeCompare([]byte(tokenString), []byte(s.adminToken)) == 1 {
		now := time.Now().UTC()
		return &Claims{
			Subject:   "admin-static",
			Username:  "admin",
			Role:      RoleAdmin,
			Issuer:    "lockguard",
			Audience:  "lockguard-api",
			IssuedAt:  now.Unix(),
			ExpiresAt: now.Add(24 * time.Hour).Unix(),
		}, nil
	}

	return ValidateJWT(tokenString, s.secretKey)
}

// RegisterUser allows administrators to provision new clients with explicit roles and bound owner IDs.
func (s *Service) RegisterUser(username, password, role, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[username]; exists {
		return fmt.Errorf("user %q already exists", username)
	}

	s.registerInternal(username, password, role, ownerID)
	return nil
}

// GetUser retrieves user account details by username.
func (s *Service) GetUser(username string) (*UserAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, exists := s.users[username]
	if !exists {
		return nil, false
	}
	clone := *u
	return &clone, true
}

func hashPassword(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + ":" + password))
	return hex.EncodeToString(h.Sum(nil))
}

func generateSalt(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

