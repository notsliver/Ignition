package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

/*
 * A minimal OpenID Connect relying party: discovery, the authorization code flow with PKCE, and
 * ID token verification (RS256/384/512, ES256) against the provider's published keys. Only what
 * sign-in needs; no access tokens are kept.
 */

var oidcClient = &http.Client{Timeout: 8 * time.Second}

type OIDCProvider struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SigningAlgs           []string `json:"id_token_signing_alg_values_supported"`

	mu      sync.Mutex
	keys    map[string]crypto.PublicKey
	fetched time.Time
}

// DiscoverOIDC reads the provider's configuration. The issuer it reports must match exactly.
func DiscoverOIDC(ctx context.Context, issuer string) (*OIDCProvider, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("the issuer must be an https:// URL")
	}
	var p OIDCProvider
	if err := getJSON(ctx, issuer+"/.well-known/openid-configuration", &p); err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}
	if strings.TrimRight(p.Issuer, "/") != issuer {
		return nil, fmt.Errorf("the provider calls itself %q, not %q", p.Issuer, issuer)
	}
	if p.AuthorizationEndpoint == "" || p.TokenEndpoint == "" || p.JWKSURI == "" {
		return nil, errors.New("the provider's configuration is missing endpoints")
	}
	return &p, nil
}

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := oidcClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// PKCE returns a verifier and its S256 challenge.
func PKCE() (verifier, challenge string) {
	raw := make([]byte, 32)
	rand.Read(raw)
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (p *OIDCProvider) AuthURL(clientID, redirect, state, nonce, challenge string) string {
	v := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "scope": {"openid email profile"},
		"state": {state}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(p.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return p.AuthorizationEndpoint + sep + v.Encode()
}

// Exchange trades the code for an ID token (client_secret_basic, which every provider supports).
func (p *OIDCProvider) Exchange(ctx context.Context, clientID, secret, code, redirect, verifier string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	resp, err := oidcClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != http.StatusOK || out.IDToken == "" {
		msg := out.Desc
		if msg == "" {
			msg = out.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("the provider refused the code: %s", msg)
	}
	return out.IDToken, nil
}

// Claims are the ID token fields sign-in uses.
type Claims struct {
	Issuer        string          `json:"iss"`
	Subject       string          `json:"sub"`
	Audience      json.RawMessage `json:"aud"`
	Expiry        int64           `json:"exp"`
	IssuedAt      int64           `json:"iat"`
	Nonce         string          `json:"nonce"`
	Email         string          `json:"email"`
	EmailVerified *bool           `json:"email_verified"`
	Name          string          `json:"name"`
}

func (c Claims) hasAudience(clientID string) bool {
	var one string
	if json.Unmarshal(c.Audience, &one) == nil {
		return one == clientID
	}
	var many []string
	json.Unmarshal(c.Audience, &many)
	for _, a := range many {
		if a == clientID {
			return true
		}
	}
	return false
}

// Verify checks the ID token's signature against the provider's keys and its issuer, audience,
// expiry and nonce.
func (p *OIDCProvider) Verify(ctx context.Context, token, clientID, nonce string, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	var header struct{ Alg, Kid string }
	if err := decodeSegment(parts[0], &header); err != nil {
		return nil, errors.New("malformed ID token header")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed ID token signature")
	}
	key, err := p.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(header.Alg, key, []byte(parts[0]+"."+parts[1]), sig); err != nil {
		return nil, err
	}
	var c Claims
	if err := decodeSegment(parts[1], &c); err != nil {
		return nil, errors.New("malformed ID token claims")
	}
	switch {
	case strings.TrimRight(c.Issuer, "/") != strings.TrimRight(p.Issuer, "/"):
		return nil, errors.New("the ID token comes from a different issuer")
	case !c.hasAudience(clientID):
		return nil, errors.New("the ID token was issued to a different client")
	case now.Unix() > c.Expiry+60:
		return nil, errors.New("the ID token has expired")
	case nonce != "" && c.Nonce != nonce:
		return nil, errors.New("the sign-in response doesn't match this request")
	case c.Subject == "":
		return nil, errors.New("the ID token has no subject")
	}
	return &c, nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func verifySignature(alg string, key crypto.PublicKey, signed, sig []byte) error {
	hashFor := map[string]crypto.Hash{"RS256": crypto.SHA256, "RS384": crypto.SHA384, "RS512": crypto.SHA512, "ES256": crypto.SHA256}
	h, ok := hashFor[alg]
	if !ok {
		return fmt.Errorf("unsupported ID token algorithm %q", alg)
	}
	hasher := h.New()
	hasher.Write(signed)
	digest := hasher.Sum(nil)
	switch k := key.(type) {
	case *rsa.PublicKey:
		if !strings.HasPrefix(alg, "RS") || rsa.VerifyPKCS1v15(k, h, digest, sig) != nil {
			return errors.New("the ID token signature is invalid")
		}
	case *ecdsa.PublicKey:
		if alg != "ES256" || len(sig) != 64 || !ecdsa.Verify(k, digest, new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return errors.New("the ID token signature is invalid")
		}
	default:
		return errors.New("unsupported signing key")
	}
	return nil
}

// key finds a signing key by ID, refetching the key set once when it's unknown (rotation).
func (p *OIDCProvider) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if k, ok := p.pick(kid); ok && time.Since(p.fetched) < time.Hour {
		return k, nil
	}
	if time.Since(p.fetched) > 10*time.Second {
		if err := p.fetchKeys(ctx); err != nil {
			return nil, err
		}
	}
	if k, ok := p.pick(kid); ok {
		return k, nil
	}
	return nil, errors.New("the ID token is signed with a key the provider doesn't publish")
}

func (p *OIDCProvider) pick(kid string) (crypto.PublicKey, bool) {
	if kid != "" {
		k, ok := p.keys[kid]
		return k, ok
	}
	if len(p.keys) == 1 {
		for _, k := range p.keys {
			return k, true
		}
	}
	return nil, false
}

func (p *OIDCProvider) fetchKeys(ctx context.Context) error {
	var set struct {
		Keys []struct {
			Kty, Kid, Use, N, E, Crv, X, Y string
		} `json:"keys"`
	}
	if err := getJSON(ctx, p.JWKSURI, &set); err != nil {
		return fmt.Errorf("couldn't read the provider's keys: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n, err1 := base64.RawURLEncoding.DecodeString(k.N)
			e, err2 := base64.RawURLEncoding.DecodeString(k.E)
			if err1 != nil || err2 != nil {
				continue
			}
			keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			x, err1 := base64.RawURLEncoding.DecodeString(k.X)
			y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			keys[k.Kid] = &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		}
	}
	p.keys, p.fetched = keys, time.Now()
	return nil
}
