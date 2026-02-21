package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Sealer provides methods to seal and unseal webhook configurations.
type Sealer struct {
	Secret string //nolint:gosec // intentional secret field
}

type sealedConfig struct {
	URL  string `json:"url"`
	Tmpl string `json:"tmpl"`
}

// Seal takes a URL and a template string, encrypts them, and returns a token that can be used to retrieve the original values later.
func (s Sealer) Seal(urlStr, tmplStr string) (string, error) {
	key := sha256.Sum256([]byte(s.Secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	plaintext, err := json.Marshal(sealedConfig{URL: urlStr, Tmpl: tmplStr})
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

// Unseal decodes the token and returns the original URL and template strings.
func (s Sealer) Unseal(token string) (urlStr, tmplStr string, err error) {
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", "", fmt.Errorf("decode token: %w", err)
	}

	key := sha256.Sum256([]byte(s.Secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", "", fmt.Errorf("token too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", "", fmt.Errorf("decrypt token: %w", err)
	}

	var cfg sealedConfig
	if err = json.Unmarshal(plaintext, &cfg); err != nil {
		return "", "", fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg.URL, cfg.Tmpl, nil
}
