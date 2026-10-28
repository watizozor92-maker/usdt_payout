package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"usdt_payout/internal/amount"
	"usdt_payout/internal/signature"
	"usdt_payout/internal/store"
	"usdt_payout/internal/tron"
)

type API struct {
	Store         *store.Store
	AppID, Secret string
	AllowedHosts  []string
}
type response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, response{Code: 0, Msg: "ok"}) })
	m.HandleFunc("POST /api/v1/withdraw", a.withdraw)
	m.HandleFunc("POST /withdraw", a.withdraw)
	m.HandleFunc("POST /api/v1/withdraw/query", a.query)
	return recoverer(logging(limitBody(m)))
}

func (a *API) withdraw(w http.ResponseWriter, r *http.Request) {
	v, e := readValues(r)
	if e != nil {
		fail(w, 400, "invalid request: "+e.Error())
		return
	}
	required := []string{"appId", "merchantOrderNo", "chainType", "crypto", "toAddress", "notify_url", "signature"}
	for _, k := range required {
		if strings.TrimSpace(v[k]) == "" {
			fail(w, 400, k+" is required")
			return
		}
	}
	if v["appId"] != a.AppID || !signature.Verify(v, a.Secret, v["signature"]) {
		fail(w, 401, "authentication failed")
		return
	}
	if v["chainType"] != "1" {
		fail(w, 400, "only chainType=1 (TRON/TRC20) is supported")
		return
	}
	_, normalized, e := amount.ParseUSDT(v["crypto"])
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !tron.ValidAddress(v["toAddress"]) {
		fail(w, 400, "invalid TRON toAddress")
		return
	}
	if len(v["merchantOrderNo"]) > 128 || len(v["attach"]) > 512 {
		fail(w, 400, "field is too long")
		return
	}
	if e = a.validateCallback(v["notify_url"]); e != nil {
		fail(w, 400, e.Error())
		return
	}
	o := &store.Order{WithdrawNo: newID(), MerchantOrderNo: v["merchantOrderNo"], AppID: v["appId"], ChainType: v["chainType"], Amount: normalized, ToAddress: v["toAddress"], Attach: v["attach"], NotifyURL: v["notify_url"]}
	created, e := a.Store.Create(r.Context(), o)
	if e != nil {
		slog.Error("create order", "error", e)
		fail(w, 500, "internal error")
		return
	}
	if !created {
		existing, e := a.Store.ByMerchant(r.Context(), o.AppID, o.MerchantOrderNo)
		if e != nil {
			fail(w, 500, "internal error")
			return
		}
		if e = existing.ValidateDuplicate(normalized, o.ToAddress, o.NotifyURL); e != nil {
			fail(w, 409, e.Error())
			return
		}
		o = existing
	}
	write(w, 200, response{Code: 0, Msg: "success", Data: map[string]any{"withdraw_no": o.WithdrawNo, "merchant_order_no": o.MerchantOrderNo, "status": publicStatus(o)}})
}

func (a *API) query(w http.ResponseWriter, r *http.Request) {
	v, e := readValues(r)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if v["appId"] != a.AppID || v["merchantOrderNo"] == "" || !signature.Verify(v, a.Secret, v["signature"]) {
		fail(w, 401, "authentication failed")
		return
	}
	o, e := a.Store.ByMerchant(r.Context(), v["appId"], v["merchantOrderNo"])
	if store.IsNotFound(e) {
		fail(w, 404, "order not found")
		return
	}
	if e != nil {
		fail(w, 500, "internal error")
		return
	}
	write(w, 200, response{Code: 0, Msg: "success", Data: map[string]any{"withdraw_no": o.WithdrawNo, "merchant_order_no": o.MerchantOrderNo, "status": publicStatus(o), "amount": o.Amount, "to_address": o.ToAddress, "txid": o.TxID, "error": o.LastError}})
}

func (a *API) validateCallback(s string) error {
	u, e := url.Parse(s)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("invalid notify_url")
	}
	if len(a.AllowedHosts) > 0 {
		host := strings.ToLower(u.Hostname())
		for _, h := range a.AllowedHosts {
			if host == h {
				return nil
			}
		}
		return errors.New("notify_url host is not allowed")
	}
	return nil
}
func readValues(r *http.Request) (map[string]string, error) {
	v := map[string]string{}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var raw map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if e := d.Decode(&raw); e != nil {
			return nil, e
		}
		for k, x := range raw {
			v[k] = strings.TrimSpace(toString(x))
		}
	} else {
		if e := r.ParseForm(); e != nil {
			return nil, e
		}
		for k := range r.Form {
			v[k] = strings.TrimSpace(r.Form.Get(k))
		}
	}
	return v, nil
}
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
func newID() string { b := make([]byte, 12); _, _ = rand.Read(b); return "wd_" + hex.EncodeToString(b) }
func publicStatus(o *store.Order) string {
	if o.Result == "success" || o.Result == "failed" {
		return o.Result
	}
	return "processing"
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, response{Code: status, Msg: msg})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "remote", remoteIP(r), "duration", time.Since(start))
	})
}
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if x := recover(); x != nil {
				slog.Error("http panic", "panic", x)
				fail(w, 500, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func remoteIP(r *http.Request) string {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return h
	}
	return r.RemoteAddr
}
