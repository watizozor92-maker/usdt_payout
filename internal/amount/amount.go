package amount

import (
	"errors"
	"strconv"
	"strings"
)

// USDT has six decimals. Parse rejects rounding so the requested and sent amounts always match.
func ParseUSDT(s string) (int64, string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, "", errors.New("invalid crypto amount")
	}
	p := strings.Split(s, ".")
	if len(p) > 2 {
		return 0, "", errors.New("invalid crypto amount")
	}
	whole := p[0]
	if whole == "" {
		whole = "0"
	}
	frac := ""
	if len(p) == 2 {
		frac = p[1]
	}
	if len(frac) > 6 {
		return 0, "", errors.New("crypto supports at most 6 decimals")
	}
	if len(whole) > 12 {
		return 0, "", errors.New("crypto amount too large")
	}
	for len(frac) < 6 {
		frac += "0"
	}
	u, e := strconv.ParseInt(whole+frac, 10, 64)
	if e != nil || u <= 0 {
		return 0, "", errors.New("crypto must be greater than zero")
	}
	frac = strings.TrimRight(frac, "0")
	normalized := whole
	if frac != "" {
		normalized += "." + frac
	}
	return u, normalized, nil
}
