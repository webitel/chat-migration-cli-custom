package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type Encryptor struct {
	keyBytes []byte
	block    cipher.Block
	gcm      cipher.AEAD
	nonce    []byte
}

func NewEncryptor(encryptionKey string) (*Encryptor, error) {
	keyBytes := []byte(encryptionKey)
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("invalid encryption key size: expected 32 bytes, got %d", len(keyBytes))
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	return &Encryptor{
		keyBytes: keyBytes,
		block:    block,
		gcm:      gcm,
		nonce:    nonce,
	}, nil
}

func (e *Encryptor) EncryptToken(token string) (string, error) {
	ciphertext := e.gcm.Seal(e.nonce, e.nonce, []byte(token), nil)
	encryptedToken := base64.StdEncoding.EncodeToString(ciphertext)

	return encryptedToken, nil
}
