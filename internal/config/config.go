package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr, DBPath, AppID, AppSecret, CallbackKey              string
	CallbackAllowedHosts                                         []string
	Driver, TronAPIURL, TronAPIKey, TronPrivateKey, TronContract string
	TronConfirmations, Workers, MaxAttempts, CallbackMaxAttempts int
	TronConfirmTimeout                                           time.Duration
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr: env("HTTP_ADDR", ":8088"), DBPath: env("DB_PATH", "./data/payout.db"),
		AppID: os.Getenv("APP_ID"), AppSecret: os.Getenv("APP_SECRET"), CallbackKey: os.Getenv("CALLBACK_KEY"),
		Driver: env("PAYOUT_DRIVER", "tron"), TronAPIURL: strings.TrimRight(env("TRON_API_URL", "https://api.trongrid.io"), "/"),
		TronAPIKey: os.Getenv("TRON_API_KEY"), TronPrivateKey: strings.TrimPrefix(os.Getenv("TRON_PRIVATE_KEY"), "0x"),
		TronContract:      env("TRON_USDT_CONTRACT", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"),
		TronConfirmations: envInt("TRON_CONFIRMATIONS", 20), Workers: envInt("WORKERS", 1),
		MaxAttempts: envInt("MAX_ATTEMPTS", 8), CallbackMaxAttempts: envInt("CALLBACK_MAX_ATTEMPTS", 20),
		TronConfirmTimeout: 10 * time.Minute,
	}
	if v := os.Getenv("TRON_CONFIRM_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("invalid TRON_CONFIRM_TIMEOUT: %w", err)
		}
		c.TronConfirmTimeout = d
	}
	for _, h := range strings.Split(os.Getenv("CALLBACK_ALLOWED_HOSTS"), ",") {
		if h = strings.TrimSpace(strings.ToLower(h)); h != "" {
			c.CallbackAllowedHosts = append(c.CallbackAllowedHosts, h)
		}
	}
	if c.AppID == "" {
		return c, errors.New("APP_ID is required")
	}
	if len(c.AppSecret) < 32 {
		return c, errors.New("APP_SECRET must contain at least 32 characters")
	}
	if c.CallbackKey == "" {
		return c, errors.New("CALLBACK_KEY is required")
	}
	if c.Workers < 1 || c.Workers > 16 {
		return c, errors.New("WORKERS must be between 1 and 16")
	}
	if c.Driver != "tron" && c.Driver != "mock" {
		return c, errors.New("PAYOUT_DRIVER must be tron or mock")
	}
	if c.Driver == "tron" && len(c.TronPrivateKey) != 64 {
		return c, errors.New("TRON_PRIVATE_KEY must be a 32-byte hex private key")
	}
	if c.Driver == "tron" && len(c.CallbackAllowedHosts) == 0 {
		return c, errors.New("CALLBACK_ALLOWED_HOSTS is required when PAYOUT_DRIVER=tron")
	}
	return c, nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	v, e := strconv.Atoi(os.Getenv(k))
	if e == nil {
		return v
	}
	return d
}
