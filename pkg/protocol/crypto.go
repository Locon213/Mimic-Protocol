package protocol

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

// DeriveKey creates a 32-byte key from a string (e.g., UUID) using SHA-256
// The derived key is suitable for ChaCha20-Poly1305
func DeriveKey(secret string) []byte {
	hash := sha256.Sum256([]byte(secret))
	return hash[:]
}

// EncryptChaCha20Poly1305 encrypts plaintext using ChaCha20-Poly1305
// Returns: [nonce(24 bytes)][ciphertext+tag]
func EncryptChaCha20Poly1305(key []byte, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create ChaCha20-Poly1305 cipher: %w", err)
	}

	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	// Prepend nonce to ciphertext for transmission
	return append(nonce, ciphertext...), nil
}

// DecryptChaCha20Poly1305 decrypts ciphertext using ChaCha20-Poly1305
// Expects format: [nonce(24 bytes)][ciphertext+tag]
func DecryptChaCha20Poly1305(key []byte, ciphertext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create ChaCha20-Poly1305 cipher: %w", err)
	}

	nonceSize := chacha20poly1305.NonceSize
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: expected at least %d bytes, got %d", nonceSize, len(ciphertext))
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// EncryptAESGCM is deprecated, use EncryptChaCha20Poly1305 instead
// Kept for backward compatibility with legacy code
// Deprecated: Use EncryptChaCha20Poly1305 for better performance and security
func EncryptAESGCM(key []byte, plaintext []byte) ([]byte, error) {
	return EncryptChaCha20Poly1305(key, plaintext)
}

// DecryptAESGCM is deprecated, use DecryptChaCha20Poly1305 instead
// Kept for backward compatibility with legacy code
// Deprecated: Use DecryptChaCha20Poly1305 for better performance and security
func DecryptAESGCM(key []byte, ciphertext []byte) ([]byte, error) {
	return DecryptChaCha20Poly1305(key, ciphertext)
}
