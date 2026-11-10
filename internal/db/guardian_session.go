package db

import (
	"context"
	"errors"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These are the existing guardian mutation keys, in their dependency order.
var guardianSessionKeys = [...]string{"mycfc/guardian-authority-policy", "mycfc/guardian-authority", "mycfc/guardian-age-handoff/v1"}

var (
	ErrSessionAdmissionContaminated = errors.New("guardian session admission contaminated")
	ErrSessionAdmissionCleanup      = errors.New("guardian session admission cleanup failed")
	ErrCommitOutcomeUnknown         = errors.New("transaction commit outcome unknown")
)

type RecipientTxOptions struct {
	AdmissionTimeout   time.Duration
	TransactionTimeout time.Duration
	CleanupTimeout     time.Duration
}

type SessionTxOutcome uint8

const (
	SessionTxNotCommitted SessionTxOutcome = iota
	SessionTxCommitted
	SessionTxOutcomeUnknown
)

type SessionTxResult struct {
	Outcome       SessionTxOutcome
	Discarded     bool
	CleanupFailed bool
}

type PolicyWriterOptions struct {
	AdmissionTimeout time.Duration
	CleanupTimeout   time.Duration
}

type PolicyWriterResult struct {
	OperationCompleted bool
	Closed             bool
	CleanupFailed      bool
}

// WithRecipientPublicationTx owns a single pool lease through pre-BEGIN SESSION
// admission, SERIALIZABLE completion, and checked cleanup. body must use only the
// supplied commands, close its rows, and never execute transaction/session control
// SQL or retain/concurrently use the facade. There is no automatic retry.
//
// Committed always returns nil error, even if subsequent cleanup discards the
// connection. Unknown must be handled before generic SQLSTATE normalization: it
// may already have committed and must not be retried from the same review.
func WithRecipientPublicationTx(ctx context.Context, pool *pgxpool.Pool, options RecipientTxOptions, body func(context.Context, dbgen.DBTX) error) (result SessionTxResult, err error) {
	if pool == nil || body == nil || options.AdmissionTimeout <= 0 || options.TransactionTimeout <= 0 || options.CleanupTimeout <= 0 {
		return result, errors.New("invalid recipient session admission options")
	}
	admissionCtx, admissionCancel := context.WithTimeout(ctx, options.AdmissionTimeout)
	defer admissionCancel()
	lease, err := pool.Acquire(admissionCtx)
	if err != nil {
		return result, err
	}
	owner := guardianSessionOwner{lease: lease, conn: lease.Conn(), cleanupTimeout: options.CleanupTimeout}
	defer func() {
		defer owner.discardOnPanic()
		if p := recover(); p != nil {
			panic(p)
		}
		owner.finishPublication(&result, &err)
	}()
	if err = owner.inspect(admissionCtx, true); err != nil {
		owner.unsafe = true
		return result, err
	}
	for _, key := range guardianSessionKeys {
		// A canceled query may have acquired its SESSION lock before acknowledgement.
		owner.unsafe = true
		if _, err = owner.conn.Exec(admissionCtx, `SELECT pg_catalog.pg_advisory_lock_shared(pg_catalog.hashtextextended($1::pg_catalog.text,0::pg_catalog.int8))`, key); err != nil {
			return result, err
		}
		owner.keys = append(owner.keys, key)
		if !owner.idle() {
			return result, ErrSessionAdmissionContaminated
		}
		owner.unsafe = false
	}
	txCtx, txCancel := context.WithTimeout(ctx, options.TransactionTimeout)
	defer txCancel()
	owner.unsafe = true
	owner.tx, err = owner.conn.BeginTx(txCtx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, err
	}
	owner.unsafe = false
	if err = body(txCtx, guardianTxCommands{tx: owner.tx}); err != nil {
		return result, err
	}
	if !owner.activeTransaction() {
		owner.unsafe = true
		result.Outcome = SessionTxOutcomeUnknown
		return result, ErrCommitOutcomeUnknown
	}
	owner.commitAttempted = true
	err = owner.tx.Commit(txCtx)
	if err == nil {
		owner.txCompleted = true
		result.Outcome = SessionTxCommitted
		return result, nil
	}
	if recipientCommitNotCommitted(err, owner.conn) {
		owner.txCompleted = true
		return result, err
	}
	owner.unsafe = true
	result.Outcome = SessionTxOutcomeUnknown
	return result, errors.Join(ErrCommitOutcomeUnknown, err)
}

// WithGuardianPolicyWriter fences the ORIGINAL one-shot mutation connection. Its
// body may contain multiple actual commits: OperationCompleted is not a promise
// of all-or-nothing CLI mutation. Never use this on a subordinate low inspector.
// Normal success leaves a checked clean connection for its caller to close;
// uncertainty closes it. There is no implicit migration-operation deadline.
func WithGuardianPolicyWriter(ctx context.Context, original *pgx.Conn, options PolicyWriterOptions, body func(context.Context, *pgx.Conn) error) (result PolicyWriterResult, err error) {
	if original == nil || body == nil || options.AdmissionTimeout <= 0 || options.CleanupTimeout <= 0 {
		return result, errors.New("invalid guardian policy writer options")
	}
	owner := guardianSessionOwner{conn: original, cleanupTimeout: options.CleanupTimeout, exclusive: true}
	defer func() {
		defer owner.cancelCleanup()
		defer owner.discardOnPanic()
		if p := recover(); p != nil {
			owner.discard()
			panic(p)
		}
		if owner.unsafe || !owner.idle() {
			owner.discard()
			result.Closed = true
			result.CleanupFailed = true
			if err == nil {
				err = ErrSessionAdmissionCleanup
			}
			return
		}
		if err != nil {
			owner.discard()
			result.Closed = true
			return
		}
		if cleanupErr := owner.unlock(); cleanupErr != nil {
			owner.discard()
			result.Closed = true
			result.CleanupFailed = true
			err = errors.Join(err, ErrSessionAdmissionCleanup)
		}
	}()
	admissionCtx, cancel := context.WithTimeout(ctx, options.AdmissionTimeout)
	defer cancel()
	if err = owner.inspect(admissionCtx, false); err != nil {
		owner.unsafe = true
		return result, err
	}
	owner.unsafe = true
	if _, err = original.Exec(admissionCtx, `SELECT pg_catalog.pg_advisory_lock(pg_catalog.hashtextextended($1::pg_catalog.text,0::pg_catalog.int8))`, guardianSessionKeys[0]); err != nil {
		return result, err
	}
	owner.keys = append(owner.keys, guardianSessionKeys[0])
	if !owner.idle() {
		return result, ErrSessionAdmissionContaminated
	}
	owner.unsafe = false
	err = body(ctx, original)
	result.OperationCompleted = err == nil
	return result, err
}

type guardianSessionOwner struct {
	lease            *pgxpool.Conn
	conn             *pgx.Conn
	tx               pgx.Tx
	keys             []string
	cleanupTimeout   time.Duration
	cleanupCtx       context.Context
	cleanupCancel    context.CancelFunc
	cleanupSQLCtx    context.Context
	cleanupSQLCancel context.CancelFunc
	sessionUser      string
	currentUser      string
	exclusive        bool
	unsafe           bool
	txCompleted      bool
	commitAttempted  bool
	disposed         bool
}

func (o *guardianSessionOwner) idle() bool {
	return o.conn != nil && !o.conn.IsClosed() && !o.conn.PgConn().IsBusy() && o.conn.PgConn().TxStatus() == 'I'
}
func (o *guardianSessionOwner) activeTransaction() bool {
	return o.conn != nil && !o.conn.IsClosed() && !o.conn.PgConn().IsBusy() && (o.conn.PgConn().TxStatus() == 'T' || o.conn.PgConn().TxStatus() == 'E')
}

// inspect uses only fixed own-backend catalogs; it never reads private guardian
// state. Publication must authenticate directly, with no inherited SET ROLE.
func (o *guardianSessionOwner) inspect(ctx context.Context, publication bool) error {
	if !o.idle() {
		return ErrSessionAdmissionContaminated
	}
	var session, current string
	var hasLocks bool
	err := o.conn.QueryRow(ctx, `SELECT session_user::pg_catalog.text,current_user::pg_catalog.text,EXISTS(SELECT 1 FROM pg_catalog.pg_locks AS lock_row WHERE lock_row.pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid() AND lock_row.locktype OPERATOR(pg_catalog.=) 'advisory'::pg_catalog.text)`).Scan(&session, &current, &hasLocks)
	if err != nil {
		return err
	}
	if !o.idle() || hasLocks || session != o.conn.Config().User || (publication && current != session) {
		return ErrSessionAdmissionContaminated
	}
	if o.sessionUser == "" {
		o.sessionUser, o.currentUser = session, current
	} else if session != o.sessionUser || current != o.currentUser {
		return ErrSessionAdmissionContaminated
	}
	return nil
}

func (o *guardianSessionOwner) cleanup() context.Context {
	if o.cleanupCtx == nil {
		started := time.Now()
		o.cleanupCtx, o.cleanupCancel = context.WithDeadline(context.Background(), started.Add(o.cleanupTimeout))
		// Reserve the final fifth for detach/close, inside the SAME total budget.
		reserve := o.cleanupTimeout / 5
		if reserve == 0 {
			reserve = time.Nanosecond
		}
		o.cleanupSQLCtx, o.cleanupSQLCancel = context.WithDeadline(o.cleanupCtx, started.Add(o.cleanupTimeout-reserve))
	}
	return o.cleanupCtx
}

func (o *guardianSessionOwner) normalCleanup() context.Context {
	_ = o.cleanup()
	return o.cleanupSQLCtx
}

func (o *guardianSessionOwner) cancelCleanup() {
	if o.cleanupSQLCancel != nil {
		o.cleanupSQLCancel()
	}
	if o.cleanupCancel != nil {
		o.cleanupCancel()
	}
}

// Also covers panics raised during rollback/unlock/inspection cleanup itself.
func (o *guardianSessionOwner) discardOnPanic() {
	if p := recover(); p != nil {
		o.discard()
		panic(p)
	}
}

func (o *guardianSessionOwner) discard() {
	if o.disposed {
		return
	}
	o.disposed = true
	if o.lease != nil {
		o.conn = o.lease.Hijack()
		o.lease = nil
	}
	// Even an expired cleanup context makes pgconn.Close close the network socket.
	// This does not promise immediate remote backend disappearance on a partition.
	_ = o.conn.Close(o.cleanup())
	o.cancelCleanup()
}

func (o *guardianSessionOwner) unlock() error {
	ctx := o.normalCleanup()
	if !o.idle() {
		return ErrSessionAdmissionCleanup
	}
	for i := len(o.keys) - 1; i >= 0; i-- {
		var unlocked bool
		sql := `SELECT pg_catalog.pg_advisory_unlock_shared(pg_catalog.hashtextextended($1::pg_catalog.text,0::pg_catalog.int8))`
		if o.exclusive {
			sql = `SELECT pg_catalog.pg_advisory_unlock(pg_catalog.hashtextextended($1::pg_catalog.text,0::pg_catalog.int8))`
		}
		if err := o.conn.QueryRow(ctx, sql, o.keys[i]).Scan(&unlocked); err != nil {
			return err
		}
		if !unlocked || !o.idle() {
			return ErrSessionAdmissionCleanup
		}
	}
	return o.inspect(ctx, !o.exclusive)
}

func (o *guardianSessionOwner) finishPublication(result *SessionTxResult, err *error) {
	if o.tx != nil && !o.txCompleted && !o.commitAttempted && !o.unsafe {
		if o.activeTransaction() {
			if rollbackErr := o.tx.Rollback(o.normalCleanup()); rollbackErr != nil {
				o.unsafe = true
			} else {
				o.txCompleted = true
			}
		} else {
			// In particular, idle/ErrTxClosed is not proof of rollback: body may have
			// incorrectly completed work through SQL, or the protocol may be lost.
			o.unsafe = true
			if o.idle() {
				result.Outcome = SessionTxOutcomeUnknown
				*err = errors.Join(ErrCommitOutcomeUnknown, *err)
			}
		}
	}
	if o.unsafe || !o.idle() {
		o.discard()
		result.Discarded = true
		result.CleanupFailed = true
	} else if cleanupErr := o.unlock(); cleanupErr != nil {
		o.discard()
		result.Discarded = true
		result.CleanupFailed = true
	} else {
		o.lease.Release()
		o.lease = nil
		o.disposed = true
		o.cancelCleanup()
	}
	if result.Outcome == SessionTxCommitted {
		*err = nil
	} else if result.CleanupFailed {
		*err = errors.Join(*err, ErrSessionAdmissionCleanup)
	}
}

func recipientCommitNotCommitted(err error, conn *pgx.Conn) bool {
	if err == pgx.ErrTxCommitRollback {
		return true
	}
	// Only the original completed server response is accepted. Wrapped/joined
	// transport errors cannot be made definitive by finding a PgError inside them.
	pgErr, ok := err.(*pgconn.PgError)
	return ok && (pgErr.Code == "40001" || pgErr.Code == "40P01") && conn != nil && !conn.IsClosed() && !conn.PgConn().IsBusy() && conn.PgConn().TxStatus() == 'I'
}

// No embedding: generated commands cannot type assert a transaction/connection.
type guardianTxCommands struct{ tx pgx.Tx }

func (q guardianTxCommands) Exec(ctx context.Context, s string, args ...any) (pgconn.CommandTag, error) {
	return q.tx.Exec(ctx, s, args...)
}
func (q guardianTxCommands) Query(ctx context.Context, s string, args ...any) (pgx.Rows, error) {
	rows, err := q.tx.Query(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return guardianCommandRows{rows: rows}, nil
}
func (q guardianTxCommands) QueryRow(ctx context.Context, s string, args ...any) pgx.Row {
	return guardianCommandRow{row: q.tx.QueryRow(ctx, s, args...)}
}

type guardianCommandRow struct{ row pgx.Row }

func (r guardianCommandRow) Scan(dest ...any) error { return r.row.Scan(dest...) }

type guardianCommandRows struct{ rows pgx.Rows }

func (r guardianCommandRows) Close()                        { r.rows.Close() }
func (r guardianCommandRows) Err() error                    { return r.rows.Err() }
func (r guardianCommandRows) CommandTag() pgconn.CommandTag { return r.rows.CommandTag() }
func (r guardianCommandRows) FieldDescriptions() []pgconn.FieldDescription {
	return r.rows.FieldDescriptions()
}
func (r guardianCommandRows) Next() bool             { return r.rows.Next() }
func (r guardianCommandRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r guardianCommandRows) Values() ([]any, error) { return r.rows.Values() }
func (r guardianCommandRows) RawValues() [][]byte    { return r.rows.RawValues() }
func (r guardianCommandRows) Conn() *pgx.Conn        { return nil }

var _ dbgen.DBTX = guardianTxCommands{}
var _ pgx.Rows = guardianCommandRows{}
