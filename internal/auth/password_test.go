package auth

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

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

func TestEqualiseVerifyCostDoesRealWork(t *testing.T) {
	// The point of EqualiseVerifyCost is the time it spends, so the test
	// is about the clock rather than a return value. A lower bound of a
	// millisecond is far below what bcrypt at the library's default cost
	// takes (tens of milliseconds) and far above what returning without
	// hashing would take, so it distinguishes the two without being
	// sensitive to how fast the machine running it is.
	start := time.Now()
	EqualiseVerifyCost("whatever an attacker typed")
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("EqualiseVerifyCost returned in %v; it did no comparison, so the miss path is still distinguishable by timing", elapsed)
	}
}

func TestPlaceholderHashCarriesTheCurrentCost(t *testing.T) {
	// A placeholder cheaper than a real hash would under-spend on the miss
	// path and reopen the timing gap it exists to close.
	cost, err := bcrypt.Cost([]byte(placeholderHash))
	if err != nil {
		t.Fatalf("placeholder is not a usable bcrypt hash: %v", err)
	}
	if cost != bcryptCost {
		t.Errorf("placeholder hash cost = %d, want %d (the cost real passwords are hashed at)", cost, bcryptCost)
	}
}

func TestPlaceholderHashMatchesNothing(t *testing.T) {
	// Nothing compares against the placeholder expecting a match, but a
	// placeholder that some plausible input *did* match would turn the
	// equalising call into an accidental authentication path if it were
	// ever wired to one by mistake.
	for _, guess := range []string{"", "password", "queuepilot placeholder", "queuepilot placeholder, matches nothing "} {
		if VerifyPassword(placeholderHash, guess) {
			t.Errorf("placeholder hash matched %q", guess)
		}
	}
}
