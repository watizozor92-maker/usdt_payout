package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"usdt_payout/internal/amount"
	"usdt_payout/internal/signature"
	"usdt_payout/internal/store"
	"usdt_payout/internal/tron"
)

type Chain interface {
	Prepare(context.Context, string, int64) (tron.Prepared, error)
	Broadcast(context.Context, string) error
	Confirm(context.Context, string) (bool, error)
}
type Worker struct {
	Store                    *store.Store
	Chain                    Chain
	Secret, CallbackKey      string
	MaxAttempts, CallbackMax int
	ConfirmTimeout           time.Duration
	HTTP                     *http.Client
}

func (w *Worker) Run(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		go w.payoutLoop(ctx)
	}
	go w.callbackLoop(ctx)
}

func (w *Worker) payoutLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		o, e := w.Store.Claim(ctx)
		if store.IsNotFound(e) {
			sleep(ctx, time.Second)
			continue
		}
		if e != nil {
			slog.Error("claim payout", "error", e)
			sleep(ctx, time.Second)
			continue
		}
		w.process(ctx, o)
	}
}
func (w *Worker) process(ctx context.Context, o *store.Order) {
	units, _, e := amount.ParseUSDT(o.Amount)
	if e != nil {
		_ = w.Store.MarkFailed(ctx, o.ID, e.Error())
		return
	}
	if o.SignedTx == "" {
		p, e := w.Chain.Prepare(ctx, o.ToAddress, units)
		if e != nil {
			w.retryOrFail(ctx, o, e)
			return
		}
		if e = w.Store.SavePrepared(ctx, o.ID, p.TxID, p.SignedJSON); e != nil {
			w.retryOrFail(ctx, o, e)
			return
		}
		o.TxID, o.SignedTx = p.TxID, p.SignedJSON
	}
	if e = w.Chain.Broadcast(ctx, o.SignedTx); e != nil {
		w.retryOrFail(ctx, o, e)
		return
	}
	deadline := time.Now().Add(w.ConfirmTimeout)
	for time.Now().Before(deadline) {
		ok, e := w.Chain.Confirm(ctx, o.TxID)
		if e != nil {
			if errors.Is(e, tron.ErrTransactionFailed) {
				_ = w.Store.MarkFailed(ctx, o.ID, e.Error())
			} else {
				_ = w.Store.Retry(ctx, o.ID, "confirming", e.Error(), 15*time.Second)
			}
			return
		}
		if ok {
			_ = w.Store.MarkSuccess(ctx, o.ID)
			slog.Info("payout confirmed", "withdraw_no", o.WithdrawNo, "txid", o.TxID)
			return
		}
		sleep(ctx, 3*time.Second)
		if ctx.Err() != nil {
			return
		}
	}
	_ = w.Store.Retry(ctx, o.ID, "confirming", "confirmation pending", 15*time.Second)
}
func (w *Worker) retryOrFail(ctx context.Context, o *store.Order, e error) {
	slog.Error("payout attempt failed", "withdraw_no", o.WithdrawNo, "attempt", o.Attempts, "error", e)
	if o.Attempts >= w.MaxAttempts {
		_ = w.Store.MarkFailed(ctx, o.ID, e.Error())
		return
	}
	_ = w.Store.Retry(ctx, o.ID, "queued", e.Error(), backoff(o.Attempts))
}

func (w *Worker) callbackLoop(ctx context.Context) {
	if w.HTTP == nil {
		w.HTTP = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		o, e := w.Store.ClaimCallback(ctx)
		if store.IsNotFound(e) {
			sleep(ctx, time.Second)
			continue
		}
		if e != nil {
			slog.Error("claim callback", "error", e)
			sleep(ctx, time.Second)
			continue
		}
		e = w.callback(ctx, o)
		if e == nil {
			_ = w.Store.CallbackDone(ctx, o.ID, o.Result == "success")
			continue
		}
		slog.Error("callback failed", "withdraw_no", o.WithdrawNo, "error", e)
		if o.CallbackAttempts >= w.CallbackMax {
			_ = w.Store.CallbackDone(ctx, o.ID, o.Result == "success")
			continue
		}
		_ = w.Store.CallbackRetry(ctx, o.ID, e.Error(), backoff(o.CallbackAttempts))
	}
}
func (w *Worker) callback(ctx context.Context, o *store.Order) error {
	status := "failed"
	if o.Result == "success" {
		status = "completed"
	}
	values := map[string]string{"username": o.AppID, "withdrawNo": o.WithdrawNo, "merchantWithdrawNo": o.MerchantOrderNo, "toAddress": o.ToAddress, "amount": o.Amount, "txid": o.TxID, "status": status, "now": fmt.Sprint(time.Now().Unix()), "callKey": w.CallbackKey}
	values["sign"] = signature.Sign(values, w.Secret)
	b, _ := json.Marshal(values)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, o.NotifyURL, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := w.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("callback HTTP %d: %s", resp.StatusCode, data)
	}
	return nil
}
func backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return time.Duration(1<<uint(n-1)) * time.Second
}
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

type MockChain struct{}

func (MockChain) Prepare(_ context.Context, to string, units int64) (tron.Prepared, error) {
	return tron.Prepared{TxID: fmt.Sprintf("mock-%d", time.Now().UnixNano()), SignedJSON: `{"mock":true}`}, nil
}
func (MockChain) Broadcast(context.Context, string) error       { return nil }
func (MockChain) Confirm(context.Context, string) (bool, error) { return true, nil }
