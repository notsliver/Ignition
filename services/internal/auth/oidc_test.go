package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerifyIDToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	jwks := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}
	srv := httptest.NewServer(http.HandlerFunc(jwks))
	defer srv.Close()
	p := &OIDCProvider{Issuer: "https://idp.example.com", JWKSURI: srv.URL}
	now := time.Now()
	good := map[string]any{"iss": "https://idp.example.com", "sub": "u1", "aud": "ignition", "exp": now.Add(time.Minute).Unix(), "nonce": "n1", "email": "a@example.com"}
	c, err := p.Verify(context.Background(), signRS256(t, key, "k1", good), "ignition", "n1", now)
	if err != nil || c.Subject != "u1" || c.Email != "a@example.com" {
		t.Fatalf("valid token rejected: %v", err)
	}
	bad := func(name string, token, nonce string) {
		if _, err := p.Verify(context.Background(), token, "ignition", nonce, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	bad("wrong key", signRS256(t, other, "k1", good), "n1")
	bad("wrong nonce", signRS256(t, key, "k1", good), "n2")
	expired := map[string]any{}
	for k, v := range good {
		expired[k] = v
	}
	expired["exp"] = now.Add(-time.Hour).Unix()
	bad("expired", signRS256(t, key, "k1", expired), "n1")
	expired["exp"], expired["aud"] = now.Add(time.Minute).Unix(), []string{"someone-else"}
	bad("audience", signRS256(t, key, "k1", expired), "n1")
	tampered := signRS256(t, key, "k1", good)
	parts := strings.Split(tampered, ".")
	evil, _ := json.Marshal(map[string]any{"iss": "https://idp.example.com", "sub": "admin", "aud": "ignition", "exp": now.Add(time.Minute).Unix(), "nonce": "n1"})
	bad("tampered claims", parts[0]+"."+base64.RawURLEncoding.EncodeToString(evil)+"."+parts[2], "n1")
}
