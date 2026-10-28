package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"usdt_payout/internal/api"
	"usdt_payout/internal/config"
	"usdt_payout/internal/store"
	"usdt_payout/internal/tron"
	"usdt_payout/internal/worker"
)

func main() {
	c, e := config.Load()
	if e != nil {
		slog.Error("configuration error", "error", e)
		os.Exit(1)
	}
	s, e := store.Open(c.DBPath)
	if e != nil {
		slog.Error("open database", "error", e)
		os.Exit(1)
	}
	defer s.Close()
	var chain worker.Chain
	if c.Driver == "mock" {
		slog.Warn("MOCK payout driver enabled; no real funds will move")
		chain = worker.MockChain{}
	} else {
		chain, e = tron.New(c.TronAPIURL, c.TronAPIKey, c.TronPrivateKey, c.TronContract, c.TronConfirmations)
		if e != nil {
			slog.Error("initialize TRON client", "error", e)
			os.Exit(1)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	w := &worker.Worker{Store: s, Chain: chain, Secret: c.AppSecret, CallbackKey: c.CallbackKey, MaxAttempts: c.MaxAttempts, CallbackMax: c.CallbackMaxAttempts, ConfirmTimeout: c.TronConfirmTimeout}
	w.Run(ctx, c.Workers)
	h := (&api.API{Store: s, AppID: c.AppID, Secret: c.AppSecret, AllowedHosts: c.CallbackAllowedHosts}).Handler()
	srv := &http.Server{Addr: c.HTTPAddr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		slog.Info("USDT payout service started", "addr", c.HTTPAddr, "driver", c.Driver)
		if e := srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			slog.Error("HTTP server", "error", e)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdown)
}
