package auth

import (
	"testing"
	"time"
)

func TestSignRoundTrips(t *testing.T) {
	secret := "shared-secret"
	tok, err := Sign(secret, "alice", []string{"me.read", "roms.read"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(tok, secret)
	if err != nil {
		t.Fatalf("minted token did not verify: %v", err)
	}
	if c.Username != "alice" || !c.HasScope("me.read") || !c.HasScope("roms.read") {
		t.Fatalf("claims = %+v", c)
	}
}

func TestSignRejectsEmptyInputs(t *testing.T) {
	if _, err := Sign("", "a", nil, time.Hour); err == nil {
		t.Error("signed with empty secret")
	}
	if _, err := Sign("s", "", nil, time.Hour); err == nil {
		t.Error("signed with empty username")
	}
}

func TestSignExpiryIsChecked(t *testing.T) {
	secret := "s"
	tok, err := Sign(secret, "bob", nil, -time.Minute) // already expired
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(tok, secret); err == nil {
		t.Error("expired minted token verified")
	}
}
