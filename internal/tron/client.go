package tron

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	secdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/mr-tron/base58"
	"golang.org/x/crypto/sha3"
)

type Client struct {
	baseURL, apiKey, contract, owner string
	key                              *secp.PrivateKey
	http                             *http.Client
	confirmations                    int
}
type Prepared struct{ TxID, SignedJSON string }

var ErrTransactionFailed = errors.New("on-chain transaction failed")

func New(baseURL, apiKey, privateHex, contract string, confirmations int) (*Client, error) {
	b, err := hex.DecodeString(privateHex)
	if err != nil || len(b) != 32 {
		return nil, errors.New("invalid TRON private key")
	}
	key := secp.PrivKeyFromBytes(b)
	c := &Client{baseURL: baseURL, apiKey: apiKey, contract: contract, key: key, http: &http.Client{Timeout: 20 * time.Second}, confirmations: confirmations}
	pub := key.PubKey().SerializeUncompressed()
	keccak := sha3.NewLegacyKeccak256()
	_, _ = keccak.Write(pub[1:])
	digest := keccak.Sum(nil)
	payload := append([]byte{0x41}, digest[12:]...)
	h1 := sha256.Sum256(payload)
	h2 := sha256.Sum256(h1[:])
	c.owner = base58.Encode(append(payload, h2[:4]...))
	return c, nil
}

func (c *Client) Prepare(ctx context.Context, to string, units int64) (Prepared, error) {
	toRaw, err := decodeAddress(to)
	if err != nil {
		return Prepared{}, err
	}
	toHex := hex.EncodeToString(toRaw[1:])
	parameter := strings.Repeat("0", 24) + toHex + fmt.Sprintf("%064x", units)
	req := map[string]any{"owner_address": c.owner, "contract_address": c.contract, "function_selector": "transfer(address,uint256)", "parameter": parameter, "fee_limit": 30_000_000, "call_value": 0, "visible": true}
	var resp struct {
		Result struct {
			Result  bool   `json:"result"`
			Message string `json:"message"`
		} `json:"result"`
		Transaction map[string]any `json:"transaction"`
	}
	if err := c.post(ctx, "/wallet/triggersmartcontract", req, &resp); err != nil {
		return Prepared{}, err
	}
	if !resp.Result.Result {
		return Prepared{}, fmt.Errorf("trigger contract rejected: %s", resp.Result.Message)
	}
	raw, _ := resp.Transaction["raw_data_hex"].(string)
	txid, _ := resp.Transaction["txID"].(string)
	rawBytes, err := hex.DecodeString(raw)
	if err != nil || raw == "" || txid == "" {
		return Prepared{}, errors.New("invalid transaction returned by TronGrid")
	}
	h := sha256.Sum256(rawBytes)
	compact := secdsa.SignCompact(c.key, h[:], false)
	recovery := compact[0] - 27
	sig := append(append([]byte{}, compact[1:]...), recovery)
	resp.Transaction["signature"] = []string{hex.EncodeToString(sig)}
	signed, err := json.Marshal(resp.Transaction)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{TxID: txid, SignedJSON: string(signed)}, nil
}

func (c *Client) Broadcast(ctx context.Context, signed string) error {
	var tx map[string]any
	if err := json.Unmarshal([]byte(signed), &tx); err != nil {
		return err
	}
	var resp struct {
		Result        bool `json:"result"`
		Code, Message string
	}
	if err := c.post(ctx, "/wallet/broadcasttransaction", tx, &resp); err != nil {
		return err
	}
	if !resp.Result && resp.Code != "DUP_TRANSACTION_ERROR" {
		return fmt.Errorf("broadcast rejected: %s %s", resp.Code, resp.Message)
	}
	return nil
}

func (c *Client) Confirm(ctx context.Context, txid string) (bool, error) {
	var info struct {
		BlockNumber int64 `json:"blockNumber"`
		Receipt     struct {
			Result string `json:"result"`
		} `json:"receipt"`
		Result string `json:"result"`
	}
	if err := c.post(ctx, "/walletsolidity/gettransactioninfobyid", map[string]string{"value": txid}, &info); err != nil {
		return false, err
	}
	if info.BlockNumber == 0 {
		return false, nil
	}
	if info.Receipt.Result != "SUCCESS" || (info.Result != "" && info.Result != "SUCCESS") {
		return false, fmt.Errorf("%w: %s", ErrTransactionFailed, info.Receipt.Result)
	}
	if c.confirmations <= 1 {
		return true, nil
	}
	var block struct {
		BlockHeader struct {
			RawData struct {
				Number int64 `json:"number"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := c.post(ctx, "/walletsolidity/getnowblock", map[string]string{}, &block); err != nil {
		return false, err
	}
	return block.BlockHeader.RawData.Number-info.BlockNumber+1 >= int64(c.confirmations), nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("TronGrid HTTP %d: %s", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode TronGrid response: %w", err)
	}
	return nil
}
