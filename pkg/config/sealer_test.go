package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealer(t *testing.T) {
	t.Run("seal and unseal round-trip", func(t *testing.T) {
		s := Sealer{Secret: "test-secret"}
		token, err := s.Seal("https://example.com/webhook", `{"msg":"{{.text}}"}`)
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		urlStr, tmplStr, err := s.Unseal(token)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/webhook", urlStr)
		assert.Equal(t, `{"msg":"{{.text}}"}`, tmplStr)
	})

	t.Run("each seal produces a different token", func(t *testing.T) {
		s := Sealer{Secret: "test-secret"}
		t1, err := s.Seal("https://example.com", "{{.v}}")
		require.NoError(t, err)
		t2, err := s.Seal("https://example.com", "{{.v}}")
		require.NoError(t, err)
		assert.NotEqual(t, t1, t2)
	})

	t.Run("unseal with wrong secret fails", func(t *testing.T) {
		s1 := Sealer{Secret: "secret-a"}
		s2 := Sealer{Secret: "secret-b"}

		token, err := s1.Seal("https://example.com", "{{.v}}")
		require.NoError(t, err)

		_, _, err = s2.Unseal(token)
		assert.Error(t, err)
	})

	t.Run("unseal invalid base64 fails", func(t *testing.T) {
		s := Sealer{Secret: "test-secret"}
		_, _, err := s.Unseal("!!!notbase64!!!")
		assert.Error(t, err)
	})

	t.Run("unseal truncated token fails", func(t *testing.T) {
		s := Sealer{Secret: "test-secret"}
		token, err := s.Seal("https://example.com", "{{.v}}")
		require.NoError(t, err)
		// keep only first 4 chars — shorter than nonce
		_, _, err = s.Unseal(token[:4])
		assert.Error(t, err)
	})

	t.Run("unseal tampered ciphertext fails", func(t *testing.T) {
		s := Sealer{Secret: "test-secret"}
		token, err := s.Seal("https://example.com", "{{.v}}")
		require.NoError(t, err)
		// flip a char in the middle of the token
		mid := len(token) / 2
		tampered := token[:mid] + strings.Map(func(r rune) rune {
			if r == 'A' {
				return 'B'
			}
			return 'A'
		}, string(token[mid:mid+1])) + token[mid+1:]
		_, _, err = s.Unseal(tampered)
		assert.Error(t, err)
	})
}
