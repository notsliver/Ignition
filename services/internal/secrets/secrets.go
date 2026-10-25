// Package secrets encrypts values at rest with AES-256-GCM (authenticated encryption).
// The key comes from IGNITION_SECRET_KEY and is never stored in the database.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal returns nonce || ciphertext. `context` is bound as additional data so a value
// copied to another row (e.g. another resource's variable) fails to decrypt.
func (b *Box) Seal(plaintext, context string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(context)), nil
}

func (b *Box) Open(sealed []byte, context string) (string, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("ciphertext too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(context))
	if err != nil {
		return "", errors.New("decrypt failed: wrong key or tampered value")
	}
	return string(plain), nil
}
