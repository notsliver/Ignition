package auth

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestTOTPRFC6238(t *testing.T) {
	// RFC 6238 appendix B, SHA-1 secret "12345678901234567890", truncated to 6 digits.
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for at, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		step, ok := VerifyTOTP(secret, want, time.Unix(at, 0))
		if !ok || step != uint64(at/30) {
			t.Errorf("t=%d: code %s rejected (step %d)", at, want, step)
		}
	}
	if _, ok := VerifyTOTP(secret, "287082", time.Unix(59+90, 0)); ok {
		t.Error("a code three steps old must be refused")
	}
	if _, ok := VerifyTOTP(secret, "28708", time.Unix(59, 0)); ok {
		t.Error("short codes must be refused")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes(10)
	if err != nil || len(codes) != 10 || len(hashes) != 10 {
		t.Fatal(codes, err)
	}
	if HashRecoveryCode(" "+codes[0]+" ") != hashes[0] || HashRecoveryCode(codes[0][:4]+codes[0][5:9]+codes[0][10:]) != hashes[0] {
		t.Error("recovery codes should match without dashes or surrounding spaces")
	}
}

func TestPasswordMin(t *testing.T) {
	if ValidatePasswordMin("short-pw", 8) != nil || ValidatePasswordMin("short-pw", 12) == nil {
		t.Error("minimum length")
	}
}
