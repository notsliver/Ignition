package secrets

import (
	"bytes"
	"testing"
)

func box(t *testing.T) *Box {
	b, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := box(t)
	sealed, err := b.Seal("s3cret", "res:KEY")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("s3cret")) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	got, err := b.Open(sealed, "res:KEY")
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestContextIsBound(t *testing.T) {
	b := box(t)
	sealed, _ := b.Seal("s3cret", "res-a:KEY")
	if _, err := b.Open(sealed, "res-b:KEY"); err == nil {
		t.Fatal("a value copied to another resource must not decrypt")
	}
}

func TestTamperingIsDetected(t *testing.T) {
	b := box(t)
	sealed, _ := b.Seal("s3cret", "ctx")
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed, "ctx"); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestWrongKey(t *testing.T) {
	sealed, _ := box(t).Seal("s3cret", "ctx")
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(sealed, "ctx"); err == nil {
		t.Fatal("a different key must not decrypt")
	}
}
