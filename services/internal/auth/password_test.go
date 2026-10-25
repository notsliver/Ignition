package auth

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword("correct horse battery staple", hash); !ok {
		t.Fatal("expected the right password to verify")
	}
	if ok, _ := VerifyPassword("wrong password entirely", hash); ok {
		t.Fatal("expected a wrong password to fail")
	}
	other, _ := HashPassword("correct horse battery staple")
	if other == hash {
		t.Fatal("hashes must be salted")
	}
}

func TestVerifyRejectsUnknownFormat(t *testing.T) {
	if _, err := VerifyPassword("x", "$2a$10$bcrypt-style"); err == nil {
		t.Fatal("expected unsupported hash formats to error")
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil {
		t.Fatal("short passwords must be rejected")
	}
	if ValidatePassword("twelve chars") != nil {
		t.Fatal("12-character passwords are allowed")
	}
}

func TestTokensAreUniqueAndHashed(t *testing.T) {
	a, ha, _ := NewToken()
	b, _, _ := NewToken()
	if a == b {
		t.Fatal("tokens must be random")
	}
	if string(HashToken(a)) != string(ha) {
		t.Fatal("hash must be deterministic for lookups")
	}
}
