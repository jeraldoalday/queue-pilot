package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}
	if hash == "correct horse battery staple" {
		t.Fatal("HashPassword did not hash the password")
	}

	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("VerifyPassword rejected the correct password")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("VerifyPassword accepted an incorrect password")
	}
}

func TestHashPasswordProducesDistinctHashesForSameInput(t *testing.T) {
	// bcrypt salts each hash, so hashing the same password twice must not
	// produce identical output — a regression here would mean the salt
	// isn't being generated per-call.
	h1, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Error("HashPassword produced identical hashes for the same input twice")
	}
}
