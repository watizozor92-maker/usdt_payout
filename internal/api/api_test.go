package api

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"usdt_payout/internal/signature"
	"usdt_payout/internal/store"
)

func TestWithdrawIsIdempotent(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &API{Store: s, AppID: "game", Secret: "0123456789abcdef", AllowedHosts: []string{"game.example"}}
	v := map[string]string{"appId": "game", "merchantOrderNo": "tt-1", "chainType": "1", "crypto": "10.500000", "toAddress": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", "attach": "7", "notify_url": "https://game.example/notify"}
	v["signature"] = signature.Sign(v, "0123456789abcdef")
	call := func() string {
		form := url.Values{}
		for k, x := range v {
			form.Set(k, x)
		}
		r := httptest.NewRequest("POST", "/api/v1/withdraw", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	first, second := call(), call()
	if first != second {
		t.Fatalf("idempotent response changed:\n%s\n%s", first, second)
	}
}
