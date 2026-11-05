package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type jwtHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

// GenerateJWT creates a cryptographically signed HMAC-SHA256 (HS256) JSON Web Token.
func GenerateJWT(claims Claims, secretKey string) (string, error) {
	if len(secretKey) < 16 {
		return "", fmt.Errorf("secret key too short: minimum 16 bytes required")
	}

	if claims.Issuer == "" {
		claims.Issuer = "lockguard"
	}
	if claims.Audience == "" {
		claims.Audience = "lockguard-api"
	}

	header := jwtHeader{
		Algorithm: "HS256",
		Type:      "JWT",
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal jwt header: %w", err)
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal jwt claims: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := headerB64 + "." + payloadB64
	signature := computeHMACSHA256([]byte(signingInput), []byte(secretKey))
	sigB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + sigB64, nil
}

// ValidateJWT verifies the signature, algorithm, and claims expiration of a JWT string.
func ValidateJWT(tokenString string, secretKey string) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	headerB64 := parts[0]
	payloadB64 := parts[1]
	sigB64 := parts[2]

	// 1. Decode and strictly validate header algorithm
	headerJSON, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid header encoding", ErrInvalidToken)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header json", ErrInvalidToken)
	}

	// SECURITY: Strictly enforce HS256 algorithm. Reject "none", asymmetric algorithms, or case anomalies.
	if header.Algorithm != "HS256" {
		return nil, ErrInvalidAlgorithm
	}

	// 2. Constant-time signature verification (prevents timing attacks)
	expectedSig := computeHMACSHA256([]byte(headerB64+"."+payloadB64), []byte(secretKey))
	actualSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid signature encoding", ErrInvalidToken)
	}

	if !hmac.Equal(expectedSig, actualSig) {
		return nil, ErrInvalidSignature
	}

	// 3. Decode claims
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid payload encoding", ErrInvalidToken)
	}

	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("%w: invalid claims json", ErrInvalidToken)
	}

	// 4. Validate time-based assertions
	if err := claims.Valid(time.Now().UTC()); err != nil {
		return nil, err
	}

	return &claims, nil
}

func computeHMACSHA256(message, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(message)
	return mac.Sum(nil)
}

