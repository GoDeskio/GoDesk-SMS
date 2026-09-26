package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Tokens issues session JWTs and signs outbound webhooks.
type Tokens struct {
	secret []byte
}

// NewTokens rejects secrets shorter than 32 characters.
func NewTokens(secret string) (*Tokens, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}
	return &Tokens{secret: []byte(secret)}, nil
}

type sessionClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

type oidcStateClaims struct {
	Verifier string `json:"verifier"`
	jwt.RegisteredClaims
}

// SessionToken is a bearer token for the dashboard and API.
func (t *Tokens) SessionToken(userID, email string) (string, error) {
	now := time.Now().UTC()
	claims := sessionClaims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    "godesk-sms",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// ParseSession returns the user id and email from a session token.
func (t *Tokens) ParseSession(token string) (string, string, error) {
	parsed, err := jwt.ParseWithClaims(token, &sessionClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", token.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil {
		return "", "", err
	}
	claims, ok := parsed.Claims.(*sessionClaims)
	if !ok || !parsed.Valid || claims.Subject == "" {
		return "", "", errors.New("invalid session")
	}
	return claims.Subject, claims.Email, nil
}

// WebhookToken matches the httpsms webhook Authorization bearer: HS256, audience
// is the webhook URL, subject is the user id. The issuer is godesk-sms.
func (t *Tokens) WebhookToken(userID, audience, signingKey string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Audience:  []string{audience},
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
		Issuer:    "godesk-sms",
		NotBefore: jwt.NewNumericDate(now.Add(-10 * time.Minute)),
		Subject:   userID,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(signingKey))
}

// OIDCStateCookie seals the PKCE verifier for the callback.
func (t *Tokens) OIDCStateCookie(state, verifier string) (string, error) {
	now := time.Now().UTC()
	claims := oidcStateClaims{
		Verifier: verifier,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   state,
			Issuer:    "godesk-sms",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// ParseOIDCState returns the state and PKCE verifier.
func (t *Tokens) ParseOIDCState(token string) (string, string, error) {
	parsed, err := jwt.ParseWithClaims(token, &oidcStateClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", token.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil {
		return "", "", err
	}
	claims, ok := parsed.Claims.(*oidcStateClaims)
	if !ok || !parsed.Valid {
		return "", "", errors.New("invalid oidc state")
	}
	return claims.Subject, claims.Verifier, nil
}

// RandomToken returns a hex secret.
func RandomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// PKCE returns a verifier and S256 challenge.
func PKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}
