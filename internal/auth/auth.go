// Package auth implements a dependency-free HS256 JWT and role-based access
// control for the dispatcher API: viewer < operator < admin.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	RoleViewer   = "viewer"   // read the live panel
	RoleOperator = "operator" // shelters, roads, SOS resolve/notify
	RoleAdmin    = "admin"    // brigade dispatch, alert execution, chaos, brigades
)

var rank = map[string]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

func ValidRole(r string) bool { return rank[r] > 0 }

// AtLeast reports whether role `have` satisfies the required role `need`.
func AtLeast(have, need string) bool { return rank[have] >= rank[need] && rank[need] > 0 }

type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
}

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpired      = errors.New("token expired")
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Sign issues a compact JWS (HS256) for the claims.
func Sign(secret []byte, c Claims) string {
	header := b64([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(c)
	signing := header + "." + b64(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + b64(mac.Sum(nil))
}

// Verify checks signature and expiry and returns the claims.
func Verify(secret []byte, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil || !ValidRole(c.Role) {
		return Claims{}, ErrInvalidToken
	}
	if c.Exp != 0 && time.Now().Unix() > c.Exp {
		return Claims{}, ErrExpired
	}
	return c, nil
}

// User is a dispatcher account. Passwords are kept as SHA-256 digests in
// memory and compared in constant time (a real deployment would use bcrypt
// and an identity provider; the middleware is the single integration point).
type User struct {
	Name string
	Role string
	hash [32]byte
}

func (u User) CheckPassword(p string) bool {
	h := sha256.Sum256([]byte(p))
	return subtle.ConstantTimeCompare(h[:], u.hash[:]) == 1
}

// ParseUsers reads "name:password:role,name2:password2:role2".
func ParseUsers(spec string) map[string]User {
	users := map[string]User{}
	for _, item := range strings.Split(spec, ",") {
		f := strings.SplitN(strings.TrimSpace(item), ":", 3)
		if len(f) != 3 || f[0] == "" || !ValidRole(f[2]) {
			continue
		}
		users[f[0]] = User{Name: f[0], Role: f[2], hash: sha256.Sum256([]byte(f[1]))}
	}
	return users
}
