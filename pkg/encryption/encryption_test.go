package encryption

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte("hello terraform state")
	blob, err := Encrypt(plain, "secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(blob, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("mismatch: %s", got)
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	blob, _ := Encrypt([]byte("data"), "secret")
	if _, err := Decrypt(blob, "wrong"); err != ErrDecryptFailed {
		t.Fatalf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestDecryptBadMagic(t *testing.T) {
	blob := make([]byte, minCipherLen)
	copy(blob, []byte("XXXXXX"))
	if _, err := Decrypt(blob, "secret"); err != ErrBadMagic {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
}

func TestEncryptRandFailure(t *testing.T) {
	orig := randReader
	randReader = bytes.NewReader(nil)
	defer func() { randReader = orig }()
	if _, err := Encrypt([]byte("x"), "secret"); err == nil {
		t.Fatal("expected error")
	}

	randReader = bytes.NewReader(make([]byte, saltSize))
	if _, err := Encrypt([]byte("x"), "secret"); err == nil {
		t.Fatal("expected error")
	}
}

func TestEncryptEmptyPassword(t *testing.T) {
	if _, err := Encrypt([]byte("x"), ""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Decrypt([]byte("x"), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestDecryptTampered(t *testing.T) {
	blob, _ := Encrypt([]byte("data"), "secret")
	blob[len(blob)-1] ^= 0xff
	if _, err := Decrypt(blob, "secret"); err != ErrDecryptFailed {
		t.Fatalf("expected ErrDecryptFailed, got %v", err)
	}
}
