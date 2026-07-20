package auth_test

import (
	"testing"

	"github.com/Stasky745/victus/internal/auth"
)

func TestNewState_UniqueAndNonEmpty(t *testing.T) {
	a, err := auth.NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	b, err := auth.NewState()
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("NewState must not return an empty string")
	}
	if a == b {
		t.Fatal("two calls to NewState produced the same value")
	}
}

func TestNewPKCEVerifier_UniqueAndNonEmpty(t *testing.T) {
	a, err := auth.NewPKCEVerifier()
	if err != nil {
		t.Fatalf("NewPKCEVerifier: %v", err)
	}
	b, err := auth.NewPKCEVerifier()
	if err != nil {
		t.Fatalf("NewPKCEVerifier: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("NewPKCEVerifier must not return an empty string")
	}
	if a == b {
		t.Fatal("two calls to NewPKCEVerifier produced the same value")
	}
	// RFC 7636 requires the code verifier to be 43-128 characters.
	if len(a) < 43 || len(a) > 128 {
		t.Errorf("PKCE verifier length %d outside RFC 7636's 43-128 range", len(a))
	}
}
