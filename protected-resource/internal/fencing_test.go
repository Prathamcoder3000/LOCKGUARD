package internal

import (
	"testing"
)

func TestFencingGuardMonotonicity(t *testing.T) {
	guard := NewFencingGuard()

	// First write with token 100 should succeed
	if err := guard.ValidateAndApply("bank-account-42", 100); err != nil {
		t.Fatalf("expected token 100 to succeed, got %v", err)
	}

	// Subsequent write with higher token 101 should succeed
	if err := guard.ValidateAndApply("bank-account-42", 101); err != nil {
		t.Fatalf("expected token 101 to succeed, got %v", err)
	}

	// Stale write with token 100 (less than 101) must fail
	if err := guard.ValidateAndApply("bank-account-42", 100); err != ErrStaleFencingToken {
		t.Fatalf("expected ErrStaleFencingToken for token 100, got %v", err)
	}

	// Stale write with identical token 101 must fail
	if err := guard.ValidateAndApply("bank-account-42", 101); err != ErrStaleFencingToken {
		t.Fatalf("expected ErrStaleFencingToken for token 101, got %v", err)
	}

	// Independent resource can use token 50
	if err := guard.ValidateAndApply("bank-account-99", 50); err != nil {
		t.Fatalf("expected token 50 for separate resource to succeed, got %v", err)
	}
}
