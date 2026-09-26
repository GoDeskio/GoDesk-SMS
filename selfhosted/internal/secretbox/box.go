package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Box seals short secrets with AES-GCM. The key is derived from a server secret
// so device passwords are not stored in plaintext.
type Box struct {
	key []byte
}

// New derives a 32-byte key from secret. secret must be non-empty.
func New(secret string) (*Box, error) {
	if secret == "" {
		return nil, errors.New("encryption secret is empty")
	}
	sum := sha256.Sum256([]byte(secret))
	return &Box{key: sum[:]}, nil
}

// Seal encrypts plaintext and returns a base64 token.
func (b *Box) Seal(plaintext string) (string, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal.
func (b *Box) Open(token string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("secret token is too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("open secret: %w", err)
	}
	return string(plain), nil
}
