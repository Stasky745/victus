package auth_test

import (
	"testing"

	"github.com/Stasky745/victus/internal/auth"
)

func TestHashPassword_CheckPassword_RoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if hash == "" {
		t.Fatal("expected a non-empty hash")
	}
	if !auth.CheckPassword(hash, "correct-horse-battery-staple") {
		t.Error("CheckPassword should accept the original password")
	}
	if auth.CheckPassword(hash, "wrong-password") {
		t.Error("CheckPassword should reject a wrong password")
	}
}
