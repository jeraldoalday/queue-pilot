package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is deliberately the library default rather than a hand-picked
// higher value: bcrypt's cost is meant to track available hardware, and
// golang.org/x/crypto keeps DefaultCost current for that purpose.
const bcryptCost = bcrypt.DefaultCost

func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether plain matches hash. It returns false (not
// an error) on a mismatch — callers should not distinguish "wrong password"
// from "hash unreadable" in their response to the user.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// placeholderHash is a real bcrypt hash of a value no one holds. It exists
// so a login attempt that cannot possibly succeed still costs what a real
// one does — see EqualiseVerifyCost.
//
// It is computed once at startup rather than written as a literal, so it
// always carries the current bcryptCost: a hard-coded hash would keep
// whatever cost was current the day it was pasted in, and the equalising
// work would drift cheaper than the real comparison it is standing in for.
var placeholderHash = mustPlaceholderHash()

func mustPlaceholderHash() string {
	// The input is arbitrary — nothing ever compares against this hash
	// expecting a match — but it is written out rather than left empty so
	// the cost of hashing it resembles the cost of hashing a password.
	hash, err := HashPassword("queuepilot placeholder, matches nothing")
	if err != nil {
		// bcrypt only fails here on an invalid cost, which is a constant
		// in this package. A process that cannot hash cannot authenticate
		// anyone, so failing at startup is better than failing per login.
		panic("auth: cannot hash the placeholder password: " + err.Error())
	}
	return hash
}

// EqualiseVerifyCost spends roughly the time a password comparison would
// have taken, for a login that was rejected before reaching one.
//
// Without it, the two rejection paths are distinguishable by a stopwatch.
// An unknown email returns as soon as the user lookup misses — no bcrypt
// work at all — while a wrong password returns some tens of milliseconds
// later, once the comparison finishes. That difference is an account
// enumeration oracle: an attacker learns which addresses have accounts
// here without ever guessing a password, and the same signal separates a
// disabled account from one that never existed. Both facts are ones the
// login response deliberately refuses to state.
//
// The equalisation is approximate, not constant-time in the cryptographic
// sense. It cannot be exact — a database miss and a database hit differ
// too — but it moves the difference from "always visible in one request"
// to "needs statistics over many", which is the practical bar for a login
// form.
func EqualiseVerifyCost(plain string) {
	_ = VerifyPassword(placeholderHash, plain)
}
