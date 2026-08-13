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
