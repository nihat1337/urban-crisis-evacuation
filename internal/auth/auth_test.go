package auth

import (
	"testing"
	"time"
)

func TestSignVerifyAndRoles(t *testing.T) {
	secret := []byte("s3cret")
	tok := Sign(secret, Claims{Sub: "op", Role: RoleOperator, Iat: time.Now().Unix(), Exp: time.Now().Add(time.Hour).Unix()})
	c, err := Verify(secret, tok)
	if err != nil || c.Sub != "op" || c.Role != RoleOperator {
		t.Fatalf("verify failed: %+v %v", c, err)
	}
	if _, err := Verify([]byte("other"), tok); err == nil {
		t.Fatal("wrong secret must fail")
	}
	if _, err := Verify(secret, tok+"x"); err == nil {
		t.Fatal("tampered signature must fail")
	}
	exp := Sign(secret, Claims{Sub: "x", Role: RoleAdmin, Exp: time.Now().Add(-time.Minute).Unix()})
	if _, err := Verify(secret, exp); err != ErrExpired {
		t.Fatalf("expected expiry error, got %v", err)
	}
	if !AtLeast(RoleAdmin, RoleOperator) || AtLeast(RoleViewer, RoleOperator) || AtLeast(RoleAdmin, "root") {
		t.Fatal("role ranking wrong")
	}
	users := ParseUsers("admin:pw:admin, op:pw2:operator,bad:x:king")
	if len(users) != 2 || !users["admin"].CheckPassword("pw") || users["admin"].CheckPassword("PW") || users["op"].Role != RoleOperator {
		t.Fatalf("users parsed wrong: %+v", users)
	}
}
