// Package encryption provides helpers for encrypting and decrypting Terraform state
// and module artefacts at rest (OpenTofu-style state encryption).
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	magic        = "TFENC1"
	saltSize     = 16
	nonceSize    = 12
	keySize      = 32
	minCipherLen = len(magic) + saltSize + nonceSize + 1
)

// Errors returned by Decrypt.
var (
	ErrBadMagic      = errors.New("encryption: bad magic header")
	ErrTooShort      = errors.New("encryption: ciphertext too short")
	ErrDecryptFailed = errors.New("encryption: decryption failed")
)

// randReader is a seam for tests.
var randReader io.Reader = rand.Reader

// deriveKey derives a 32-byte key from a password and salt using SHA-256.
// A production deployment should prefer a memory-hard KDF; this keeps the
// helper dependency-free and deterministic for tests.
func deriveKey(password string, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte(password))
	h.Write(salt)
	sum := h.Sum(nil)
	// Stretch slightly to avoid trivial single-pass usage.
	for i := 0; i < 1000; i++ {
		h.Reset()
		h.Write(sum)
		h.Write([]byte(password))
		h.Write(salt)
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(i)))
		sum = h.Sum(nil)
	}
	out := make([]byte, keySize)
	copy(out, sum)
	return out
}

// Encrypt encrypts plaintext with a password-derived key using AES-256-GCM.
// The returned blob is self-describing and safe to store on disk.
func Encrypt(plaintext []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("encryption: password must not be empty")
	}
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(randReader, salt); err != nil {
		return nil, fmt.Errorf("encryption: read salt: %w", err)
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return nil, fmt.Errorf("encryption: read nonce: %w", err)
	}
	key := deriveKey(password, salt)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	out := make([]byte, 0, len(magic)+saltSize+nonceSize+len(ciphertext))
	out = append(out, []byte(magic)...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// Decrypt decrypts a blob produced by Encrypt.
func Decrypt(blob []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("encryption: password must not be empty")
	}
	if len(blob) < minCipherLen {
		return nil, ErrTooShort
	}
	if string(blob[:len(magic)]) != magic {
		return nil, ErrBadMagic
	}
	offset := len(magic)
	salt := blob[offset : offset+saltSize]
	offset += saltSize
	nonce := blob[offset : offset+nonceSize]
	offset += nonceSize
	ciphertext := blob[offset:]

	key := deriveKey(password, salt)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}
