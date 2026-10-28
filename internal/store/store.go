package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Order struct {
	ID                                                                                                                             int64
	WithdrawNo, MerchantOrderNo, AppID, ChainType, Amount, ToAddress, Attach, NotifyURL, Status, Result, TxID, SignedTx, LastError string
	Attempts, CallbackAttempts                                                                                                     int
	NextAttemptAt, CreatedAt, UpdatedAt                                                                                            int64
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	for _, q := range []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`, `PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS payout_orders (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, withdraw_no TEXT NOT NULL UNIQUE, merchant_order_no TEXT NOT NULL,
		 app_id TEXT NOT NULL, chain_type TEXT NOT NULL, amount TEXT NOT NULL, to_address TEXT NOT NULL,
		 attach TEXT NOT NULL DEFAULT '', notify_url TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued',
		 result TEXT NOT NULL DEFAULT '', txid TEXT NOT NULL DEFAULT '', signed_tx TEXT NOT NULL DEFAULT '', last_error TEXT NOT NULL DEFAULT '',
		 attempts INTEGER NOT NULL DEFAULT 0, callback_attempts INTEGER NOT NULL DEFAULT 0,
		 next_attempt_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		 UNIQUE(app_id, merchant_order_no))`,
		`CREATE INDEX IF NOT EXISTS idx_payout_queue ON payout_orders(status,next_attempt_at,id)`,
	} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	_, _ = db.Exec(`ALTER TABLE payout_orders ADD COLUMN result TEXT NOT NULL DEFAULT ''`)
	// A process crash must resume the exact persisted signed transaction, never create a second transfer.
	_, _ = db.Exec(`UPDATE payout_orders SET status='queued', updated_at=? WHERE status='processing'`, time.Now().Unix())
	_, _ = db.Exec(`UPDATE payout_orders SET status='callback_pending', updated_at=? WHERE status='callback_sending'`, time.Now().Unix())
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Create(ctx context.Context, o *Order) (bool, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO payout_orders
	(withdraw_no,merchant_order_no,app_id,chain_type,amount,to_address,attach,notify_url,status,created_at,updated_at)
	VALUES(?,?,?,?,?,?,?,?, 'queued',?,?)`, o.WithdrawNo, o.MerchantOrderNo, o.AppID, o.ChainType, o.Amount, o.ToAddress, o.Attach, o.NotifyURL, now, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) ByMerchant(ctx context.Context, appID, merchant string) (*Order, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,withdraw_no,merchant_order_no,app_id,chain_type,amount,to_address,attach,notify_url,status,result,txid,signed_tx,last_error,attempts,callback_attempts,next_attempt_at,created_at,updated_at FROM payout_orders WHERE app_id=? AND merchant_order_no=?`, appID, merchant)
	return scan(row)
}

func (s *Store) Claim(ctx context.Context) (*Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	row := tx.QueryRowContext(ctx, `SELECT id,withdraw_no,merchant_order_no,app_id,chain_type,amount,to_address,attach,notify_url,status,result,txid,signed_tx,last_error,attempts,callback_attempts,next_attempt_at,created_at,updated_at FROM payout_orders WHERE status IN ('queued','confirming') AND next_attempt_at<=? ORDER BY id LIMIT 1`, now)
	o, err := scan(row)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE payout_orders SET status='processing',attempts=attempts+1,updated_at=? WHERE id=? AND status=?`, now, o.ID, o.Status)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, sql.ErrNoRows
	}
	o.Status = "processing"
	o.Attempts++
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) SavePrepared(ctx context.Context, id int64, txid, signed string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET txid=?,signed_tx=?,updated_at=? WHERE id=?`, txid, signed, time.Now().Unix(), id)
	return e
}
func (s *Store) MarkSuccess(ctx context.Context, id int64) error {
	_, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET status='callback_pending',result='success',last_error='',next_attempt_at=0,updated_at=? WHERE id=?`, time.Now().Unix(), id)
	return e
}
func (s *Store) MarkFailed(ctx context.Context, id int64, msg string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET status='callback_pending',result='failed',last_error=?,next_attempt_at=0,updated_at=? WHERE id=?`, msg, time.Now().Unix(), id)
	return e
}
func (s *Store) Retry(ctx context.Context, id int64, status, msg string, delay time.Duration) error {
	_, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET status=?,last_error=?,next_attempt_at=?,updated_at=? WHERE id=?`, status, msg, time.Now().Add(delay).Unix(), time.Now().Unix(), id)
	return e
}

func (s *Store) ClaimCallback(ctx context.Context) (*Order, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,withdraw_no,merchant_order_no,app_id,chain_type,amount,to_address,attach,notify_url,status,result,txid,signed_tx,last_error,attempts,callback_attempts,next_attempt_at,created_at,updated_at FROM payout_orders WHERE status='callback_pending' AND next_attempt_at<=? ORDER BY id LIMIT 1`, time.Now().Unix())
	o, e := scan(row)
	if e != nil {
		return nil, e
	}
	res, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET status='callback_sending',callback_attempts=callback_attempts+1,updated_at=? WHERE id=? AND status='callback_pending'`, time.Now().Unix(), o.ID)
	if e != nil {
		return nil, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, sql.ErrNoRows
	}
	o.CallbackAttempts++
	return o, nil
}
func (s *Store) CallbackDone(ctx context.Context, id int64, success bool) error {
	status := "failed"
	if success {
		status = "success"
	}
	_, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET status=?,updated_at=? WHERE id=?`, status, time.Now().Unix(), id)
	return e
}
func (s *Store) CallbackRetry(ctx context.Context, id int64, msg string, delay time.Duration) error {
	_, e := s.db.ExecContext(ctx, `UPDATE payout_orders SET status='callback_pending',last_error=CASE WHEN txid='' THEN ? ELSE last_error END,next_attempt_at=?,updated_at=? WHERE id=?`, msg, time.Now().Add(delay).Unix(), time.Now().Unix(), id)
	return e
}

type scanner interface{ Scan(...any) error }

func scan(r scanner) (*Order, error) {
	o := new(Order)
	e := r.Scan(&o.ID, &o.WithdrawNo, &o.MerchantOrderNo, &o.AppID, &o.ChainType, &o.Amount, &o.ToAddress, &o.Attach, &o.NotifyURL, &o.Status, &o.Result, &o.TxID, &o.SignedTx, &o.LastError, &o.Attempts, &o.CallbackAttempts, &o.NextAttemptAt, &o.CreatedAt, &o.UpdatedAt)
	if e != nil {
		return nil, e
	}
	return o, nil
}
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func (o *Order) ValidateDuplicate(amount, address, notify string) error {
	if o.Amount != amount || o.ToAddress != address || o.NotifyURL != notify {
		return fmt.Errorf("merchantOrderNo already exists with different parameters")
	}
	return nil
}
