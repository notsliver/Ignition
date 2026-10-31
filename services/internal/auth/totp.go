package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) with the parameters every authenticator app supports: SHA-1, 6 digits, 30 s.
const (
	totpStep   = 30
	totpDigits = 1000000
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret in base32, as authenticator apps expect.
func NewTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return b32.EncodeToString(raw), nil
}

// TOTPURL is the otpauth:// link an authenticator app scans.
func TOTPURL(issuer, account, secret string) string {
	v := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func totpAt(secret string, counter uint64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%totpDigits), nil
}

// VerifyTOTP accepts the current code and one step either side (clock drift). It returns the
// matched time step so callers can refuse a code that was already used.
func VerifyTOTP(secret, code string, now time.Time) (uint64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	step := uint64(now.Unix() / totpStep)
	for _, c := range []uint64{step, step - 1, step + 1} {
		want, err := totpAt(secret, c)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// NewRecoveryCodes returns n one-time codes like "4f1c-9a2e-77b0" and their hashes for storage.
func NewRecoveryCodes(n int) (codes []string, hashes []string, err error) {
	for range n {
		raw := make([]byte, 6)
		if _, err = rand.Read(raw); err != nil {
			return nil, nil, err
		}
		h := hex.EncodeToString(raw)
		code := h[0:4] + "-" + h[4:8] + "-" + h[8:12]
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}

// HashRecoveryCode normalises (case, dashes, spaces) before hashing so people can type them loosely.
func HashRecoveryCode(code string) string {
	norm := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	return hex.EncodeToString(HashToken(norm))
}
