package signature

import "testing"

func TestCanonicalAndVerify(t *testing.T) {
	v := map[string]string{"merchantOrderNo": "tt1", "appId": "game", "crypto": "10.5", "signature": "ignored"}
	if got := Canonical(v); got != "appId=game&crypto=10.5&merchantOrderNo=tt1" {
		t.Fatal(got)
	}
	s := Sign(v, "secret")
	if !Verify(v, "secret", s) {
		t.Fatal("valid signature rejected")
	}
	if Verify(v, "wrong", s) {
		t.Fatal("invalid signature accepted")
	}
}
