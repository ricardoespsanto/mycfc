//go:build integration

package db

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recipientSessionFixture struct {
	root *pgx.Conn
	raw  string
}

func newRecipientSessionFixture(t *testing.T) recipientSessionFixture {
	t.Helper()
	name := "mycfc-recipient-lease-" + uuid.NewString()[:8]
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P", "-e", "POSTGRES_DB=recipient_test", "-e", "POSTGRES_USER=acceptance", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9").Run(); err != nil {
		t.Fatal("isolated recipient PostgreSQL startup failed")
	}
	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", name).Run(); err != nil {
			t.Error("isolated recipient PostgreSQL cleanup failed")
		}
	})
	out, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	port := line[strings.LastIndex(line, ":")+1:]
	raw := "postgres://acceptance:" + password + "@127.0.0.1:" + port + "/recipient_test?sslmode=disable"
	var root *pgx.Conn
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		root, err = pgx.Connect(t.Context(), raw)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("isolated recipient PostgreSQL not ready")
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	_, err = root.Exec(t.Context(), `CREATE ROLE recipient_web LOGIN PASSWORD 'disposable-web-only' NOSUPERUSER NOCREATEROLE NOCREATEDB NOREPLICATION NOBYPASSRLS;
 CREATE ROLE recipient_alt NOLOGIN;
 GRANT recipient_alt TO recipient_web;
 CREATE TABLE public.recipient_admission_probe(id integer PRIMARY KEY,value integer NOT NULL);
 INSERT INTO public.recipient_admission_probe VALUES(1,0);
 GRANT SELECT,INSERT,UPDATE,DELETE ON public.recipient_admission_probe TO recipient_web;
 CREATE FUNCTION public.recipient_test_commit_error() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.value=40001 THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='test serial conflict'; END IF;
  IF NEW.value=40002 THEN RAISE EXCEPTION USING ERRCODE='40P01',MESSAGE='test deadlock'; END IF;
  RETURN NEW;
 END;$$;
 CREATE CONSTRAINT TRIGGER recipient_test_commit_error AFTER INSERT ON recipient_admission_probe DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION recipient_test_commit_error()`)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword("recipient_web", "disposable-web-only")
	return recipientSessionFixture{root, parsed.String()}
}
func (f recipientSessionFixture) pool(t *testing.T, tracer pgx.QueryTracer, raw string) *pgxpool.Pool {
	t.Helper()
	if raw == "" {
		raw = f.raw
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.Tracer = tracer
	cfg.ConnConfig.RuntimeParams["application_name"] = "recipient-session-tests"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func (f recipientSessionFixture) original(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), f.raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}
func recipientTestOptions() RecipientTxOptions {
	return RecipientTxOptions{3 * time.Second, 3 * time.Second, 2 * time.Second}
}
func recipientWriterOptions() PolicyWriterOptions {
	return PolicyWriterOptions{3 * time.Second, 2 * time.Second}
}
func recipientExec(t *testing.T, q dbgen.DBTX, s string, args ...any) {
	t.Helper()
	if _, err := q.Exec(t.Context(), s, args...); err != nil {
		t.Fatal(err)
	}
}
func recipientPID(t *testing.T, pool *pgxpool.Pool) uint32 {
	t.Helper()
	lease, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	return lease.Conn().PgConn().PID()
}
func recipientClean(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	lease, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	var clean bool
	if err = lease.QueryRow(t.Context(), `SELECT current_user=session_user AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=pg_backend_pid() AND locktype='advisory')`).Scan(&clean); err != nil {
		t.Fatal(err)
	}
	if !clean || lease.Conn().PgConn().TxStatus() != 'I' || lease.Conn().PgConn().IsBusy() {
		t.Fatal("poisoned lease returned to pool")
	}
}
func recipientWait(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("recipient test condition timed out")
}
func recipientWaitLock(t *testing.T, root *pgx.Conn, pid uint32) {
	t.Helper()
	recipientWait(t, func() bool {
		var blocked bool
		err := root.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, pid).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		return blocked
	})
}

type recipientRun struct {
	result SessionTxResult
	err    error
}

func recipientAwait(t *testing.T, ch <-chan recipientRun) recipientRun {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(6 * time.Second):
		t.Fatal("publication did not finish")
		return recipientRun{}
	}
}

func TestGuardianSessionPooledPostgres(t *testing.T) {
	f := newRecipientSessionFixture(t)
	t.Run("fresh_snapshot_after_each_completed_autocommit_admission", func(t *testing.T) {
		for _, key := range guardianSessionKeys {
			t.Run(key, func(t *testing.T) {
				pool := f.pool(t, nil, "")
				pid := recipientPID(t, pool)
				recipientExec(t, f.root, `UPDATE recipient_admission_probe SET value=0 WHERE id=1`)
				recipientExec(t, f.root, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key)
				done := make(chan recipientRun, 1)
				var seen int
				go func() {
					result, err := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
						var isolation string
						if err := q.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
							return err
						}
						if isolation != "serializable" {
							return errors.New("wrong isolation")
						}
						var actual uint32
						if err := q.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&actual); err != nil {
							return err
						}
						if actual != pid {
							return errors.New("lease backend changed")
						}
						rows, err := q.Query(ctx, `SELECT value FROM recipient_admission_probe WHERE id=1`)
						if err != nil {
							return err
						}
						defer rows.Close()
						if rows.Conn() != nil {
							return errors.New("facade leaked raw connection")
						}
						if !rows.Next() {
							return errors.New("missing probe")
						}
						if err = rows.Scan(&seen); err != nil {
							return err
						}
						rows.Close()
						return rows.Err()
					})
					done <- recipientRun{result, err}
				}()
				recipientWaitLock(t, f.root, pid)
				recipientExec(t, f.root, `UPDATE recipient_admission_probe SET value=23 WHERE id=1`)
				recipientExec(t, f.root, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
				got := recipientAwait(t, done)
				if got.err != nil || got.result.Outcome != SessionTxCommitted || seen != 23 {
					t.Fatalf("fresh admission failed: %+v %v value=%d", got.result, got.err, seen)
				}
				recipientClean(t, pool)
				if recipientPID(t, pool) != pid {
					t.Fatal("clean successful lease was needlessly discarded")
				}
			})
		}
	})
	t.Run("original_writer_waits_through_publication_commit", func(t *testing.T) {
		pool := f.pool(t, nil, "")
		original := f.original(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan recipientRun, 1)
		go func() {
			r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			done <- recipientRun{r, e}
		}()
		<-entered
		var called atomic.Bool
		writerDone := make(chan error, 1)
		go func() {
			_, e := WithGuardianPolicyWriter(t.Context(), original, recipientWriterOptions(), func(ctx context.Context, c *pgx.Conn) error {
				called.Store(true)
				_, err := c.Exec(ctx, `UPDATE recipient_admission_probe SET value=value+1 WHERE id=1`)
				return err
			})
			writerDone <- e
		}()
		recipientWaitLock(t, f.root, original.PgConn().PID())
		if called.Load() {
			t.Fatal("writer crossed publication fence")
		}
		close(release)
		if r := recipientAwait(t, done); r.err != nil || r.result.Outcome != SessionTxCommitted {
			t.Fatalf("publication failed: %+v", r)
		}
		select {
		case err := <-writerDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("writer stayed blocked")
		}
		recipientClean(t, pool)
	})
	t.Run("known_body_failure_rolls_back_and_reuses", func(t *testing.T) {
		pool := f.pool(t, nil, "")
		pid := recipientPID(t, pool)
		bodyErr := errors.New("review changed")
		r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
			if _, err := q.Exec(ctx, `INSERT INTO recipient_admission_probe VALUES(20,1)`); err != nil {
				return err
			}
			return bodyErr
		})
		if !errors.Is(e, bodyErr) || r.Outcome != SessionTxNotCommitted || r.Discarded {
			t.Fatalf("bad rollback result %+v %v", r, e)
		}
		var exists bool
		if err := f.root.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM recipient_admission_probe WHERE id=20)`).Scan(&exists); err != nil || exists {
			t.Fatal("known rollback retained row")
		}
		recipientClean(t, pool)
		if recipientPID(t, pool) != pid {
			t.Fatal("known rollback discarded clean lease")
		}
	})
	t.Run("caller_cancel_does_not_cancel_rollback_cleanup", func(t *testing.T) {
		pool := f.pool(t, nil, "")
		pid := recipientPID(t, pool)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		r, e := WithRecipientPublicationTx(ctx, pool, recipientTestOptions(), func(txCtx context.Context, q dbgen.DBTX) error {
			if _, err := q.Exec(txCtx, `INSERT INTO recipient_admission_probe VALUES(21,1)`); err != nil {
				return err
			}
			cancel()
			return ctx.Err()
		})
		if !errors.Is(e, context.Canceled) || r.Outcome != SessionTxNotCommitted || r.Discarded || r.CleanupFailed {
			t.Fatalf("cleanup inherited caller cancellation %+v %v", r, e)
		}
		recipientClean(t, pool)
		if recipientPID(t, pool) != pid {
			t.Fatal("independent successful cleanup discarded lease")
		}
	})
	t.Run("definitive_commit_rejections_and_failed_rollback", func(t *testing.T) {
		for i, value := range []int{40001, 40002, 0} {
			t.Run(fmt.Sprint(value), func(t *testing.T) {
				pool := f.pool(t, nil, "")
				pid := recipientPID(t, pool)
				r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
					if value == 0 {
						_, _ = q.Exec(ctx, `SELECT 1/0`)
						return nil
					}
					_, err := q.Exec(ctx, `INSERT INTO recipient_admission_probe VALUES($1,$2)`, 50+i, value)
					return err
				})
				if e == nil || r.Outcome != SessionTxNotCommitted || r.Discarded || errors.Is(e, ErrCommitOutcomeUnknown) {
					t.Fatalf("definitive rejection lost %+v %v", r, e)
				}
				recipientClean(t, pool)
				if recipientPID(t, pool) != pid {
					t.Fatal("known rejected commit discarded clean lease")
				}
			})
		}
		tracer := &recipientFaultTracer{beforeSQL: "rollback"}
		pool := f.pool(t, tracer, "")
		bodyErr := errors.New("review refused")
		r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { return bodyErr })
		if !errors.Is(e, bodyErr) || !r.Discarded || !r.CleanupFailed || r.Outcome != SessionTxNotCommitted {
			t.Fatalf("rollback failure hidden %+v %v", r, e)
		}
		recipientClean(t, pool)
	})
	t.Run("partial_admission_cancellation_discards_without_lock_leak", func(t *testing.T) {
		for _, key := range guardianSessionKeys[:] {
			t.Run(key, func(t *testing.T) {
				pool := f.pool(t, nil, "")
				pid := recipientPID(t, pool)
				recipientExec(t, f.root, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan recipientRun, 1)
				go func() {
					r, e := WithRecipientPublicationTx(ctx, pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { return errors.New("body must not execute") })
					done <- recipientRun{r, e}
				}()
				recipientWaitLock(t, f.root, pid)
				cancel()
				got := recipientAwait(t, done)
				if got.err == nil || !got.result.Discarded || got.result.Outcome != SessionTxNotCommitted {
					t.Fatalf("bad partial cancel %+v", got)
				}
				recipientExec(t, f.root, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
				recipientClean(t, pool)
				if recipientPID(t, pool) == pid {
					t.Fatal("uncertain acquired connection reborrowed")
				}
				recipientWait(t, func() bool {
					var n int
					if err := f.root.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory'`, pid).Scan(&n); err != nil {
						t.Fatal(err)
					}
					return n == 0
				})
			})
		}
	})
	t.Run("borrow_contamination_discarded_not_repaired", func(t *testing.T) {
		for _, sql := range []string{`SELECT pg_advisory_lock(919191)`, `SET ROLE recipient_alt`} {
			t.Run(sql, func(t *testing.T) {
				pool := f.pool(t, nil, "")
				lease, err := pool.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				pid := lease.Conn().PgConn().PID()
				recipientExec(t, lease, sql)
				lease.Release()
				r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { t.Error("contaminated body executed"); return nil })
				if !errors.Is(e, ErrSessionAdmissionContaminated) || !r.Discarded {
					t.Fatalf("contamination accepted: %+v %v", r, e)
				}
				recipientClean(t, pool)
				if recipientPID(t, pool) == pid {
					t.Fatal("contaminated backend reborrowed")
				}
			})
		}
	})
	t.Run("acknowledged_commit_survives_extra_lock_and_false_unlock", func(t *testing.T) {
		for i, sql := range []string{`SELECT pg_advisory_lock_shared(hashtextextended('mycfc/guardian-authority-policy',0))`, `SELECT pg_advisory_unlock_shared(hashtextextended('mycfc/guardian-age-handoff/v1',0))`} {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				pool := f.pool(t, nil, "")
				pid := recipientPID(t, pool)
				r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
					if _, err := q.Exec(ctx, `INSERT INTO recipient_admission_probe VALUES($1,1)`, 30+i); err != nil {
						return err
					}
					_, err := q.Exec(ctx, sql)
					return err
				})
				if e != nil || r.Outcome != SessionTxCommitted || !r.Discarded || !r.CleanupFailed {
					t.Fatalf("known commit lost: %+v %v", r, e)
				}
				var exists bool
				if err := f.root.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM recipient_admission_probe WHERE id=$1)`, 30+i).Scan(&exists); err != nil || !exists {
					t.Fatal("acknowledged committed row missing")
				}
				recipientClean(t, pool)
				if recipientPID(t, pool) == pid {
					t.Fatal("extra lock backend reborrowed")
				}
			})
		}
	})
	t.Run("panic_discards_and_propagates", func(t *testing.T) {
		pool := f.pool(t, nil, "")
		pid := recipientPID(t, pool)
		func() {
			defer func() {
				if recover() != "body panic" {
					t.Error("panic changed")
				}
			}()
			_, _ = WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { panic("body panic") })
		}()
		recipientClean(t, pool)
		if recipientPID(t, pool) == pid {
			t.Fatal("panicked lease reborrowed")
		}
	})
	t.Run("cleanup_panic_discards_before_propagating", func(t *testing.T) {
		pool := f.pool(t, &recipientFaultTracer{panicSQL: "unlock"}, "")
		pid := recipientPID(t, pool)
		func() {
			defer func() {
				if recover() != "cleanup panic" {
					t.Error("cleanup panic changed")
				}
			}()
			_, _ = WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { return nil })
		}()
		recipientClean(t, pool)
		if recipientPID(t, pool) == pid {
			t.Fatal("cleanup-panic lease reborrowed")
		}
	})
	t.Run("acknowledged_commit_then_connection_failure", func(t *testing.T) {
		tracer := &recipientFaultTracer{afterSQL: "commit"}
		pool := f.pool(t, tracer, "")
		r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
			_, err := q.Exec(ctx, `INSERT INTO recipient_admission_probe VALUES(40,1)`)
			return err
		})
		if e != nil || r.Outcome != SessionTxCommitted || !r.Discarded {
			t.Fatalf("acknowledged commit downgraded %+v %v", r, e)
		}
		recipientClean(t, pool)
	})
	t.Run("unknown_commit_can_already_have_committed", func(t *testing.T) {
		raw, ack := recipientDropCommitProxy(t, f.raw)
		pool := f.pool(t, nil, raw)
		r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(ctx context.Context, q dbgen.DBTX) error {
			_, err := q.Exec(ctx, `INSERT INTO recipient_admission_probe VALUES(41,1)`)
			return err
		})
		if !errors.Is(e, ErrCommitOutcomeUnknown) || r.Outcome != SessionTxOutcomeUnknown || !r.Discarded {
			t.Fatalf("unknown commit misclassified %+v %v", r, e)
		}
		select {
		case <-ack:
		case <-time.After(time.Second):
			t.Fatal("proxy did not observe server commit acknowledgement")
		}
		var exists bool
		if err := f.root.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM recipient_admission_probe WHERE id=41)`).Scan(&exists); err != nil || !exists {
			t.Fatal("lost commit acknowledgement fixture did not commit")
		}
	})
	t.Run("faults_around_begin_commit_and_each_unlock", func(t *testing.T) {
		sqls := []string{"begin isolation level serializable", "commit"}
		for _, key := range guardianSessionKeys {
			sqls = append(sqls, "unlock:"+key)
		}
		for _, sql := range sqls {
			t.Run(sql, func(t *testing.T) {
				tracer := &recipientFaultTracer{beforeSQL: sql}
				pool := f.pool(t, tracer, "")
				r, e := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { return nil })
				if !r.Discarded {
					t.Fatalf("faulted connection returned %+v %v", r, e)
				}
				if strings.HasPrefix(sql, "unlock:") {
					if r.Outcome != SessionTxCommitted || e != nil {
						t.Fatalf("cleanup fault downgraded commit %+v %v", r, e)
					}
				} else if sql == "commit" {
					if r.Outcome != SessionTxOutcomeUnknown || !errors.Is(e, ErrCommitOutcomeUnknown) {
						t.Fatalf("commit transport failure not unknown %+v %v", r, e)
					}
				} else if r.Outcome != SessionTxNotCommitted || e == nil {
					t.Fatalf("BEGIN fault result %+v %v", r, e)
				}
				recipientClean(t, pool)
			})
		}
	})
	t.Run("privileged_original_does_not_reacquire_on_low_inspector", func(t *testing.T) {
		low := f.original(t)
		r, e := WithGuardianPolicyWriter(t.Context(), f.root, recipientWriterOptions(), func(ctx context.Context, original *pgx.Conn) error {
			if original != f.root {
				return errors.New("original privileged handle replaced")
			}
			if original.PgConn().PID() == low.PgConn().PID() {
				return errors.New("inspector is not separately authenticated")
			}
			readTx, err := low.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
			if err != nil {
				return err
			}
			defer readTx.Rollback(context.Background())
			var value int
			if err = readTx.QueryRow(ctx, `SELECT value FROM recipient_admission_probe WHERE id=1`).Scan(&value); err != nil {
				return err
			}
			return readTx.Commit(ctx)
		})
		if e != nil || !r.OperationCompleted || r.Closed {
			t.Fatalf("low inspector recursively blocked %+v %v", r, e)
		}
	})
	t.Run("original_writer_error_preserves_prior_autocommit", func(t *testing.T) {
		original := f.original(t)
		bodyErr := errors.New("later CLI step refused")
		r, e := WithGuardianPolicyWriter(t.Context(), original, recipientWriterOptions(), func(ctx context.Context, c *pgx.Conn) error {
			if _, err := c.Exec(ctx, `INSERT INTO recipient_admission_probe VALUES(60,1)`); err != nil {
				return err
			}
			return bodyErr
		})
		if !errors.Is(e, bodyErr) || !r.Closed || r.OperationCompleted {
			t.Fatalf("CLI failure semantics lost %+v %v", r, e)
		}
		var exists bool
		if err := f.root.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM recipient_admission_probe WHERE id=60)`).Scan(&exists); err != nil || !exists {
			t.Fatal("earlier CLI autocommit should survive later error")
		}
	})
	t.Run("privileged_writer_catalog_queries_ignore_hostile_search_path", func(t *testing.T) {
		recipientExec(t, f.root, `GRANT CREATE ON SCHEMA public TO recipient_web;
   SET ROLE recipient_web;
   CREATE TABLE public.recipient_search_path_calls(actor pg_catalog.name,kind pg_catalog.text);
   CREATE FUNCTION public.recipient_type_canary() RETURNS pg_catalog.bool LANGUAGE plpgsql AS $$BEGIN
    INSERT INTO public.recipient_search_path_calls VALUES(current_user,'domain');RETURN true;END;$$;
   CREATE DOMAIN public.text AS pg_catalog.text CHECK(public.recipient_type_canary());
   CREATE FUNCTION public.recipient_int_equal(pg_catalog.int4,pg_catalog.int4) RETURNS pg_catalog.bool LANGUAGE plpgsql AS $$BEGIN
    INSERT INTO public.recipient_search_path_calls VALUES(current_user,'integer operator');RETURN $1 OPERATOR(pg_catalog.=) $2;END;$$;
   CREATE OPERATOR public.= (LEFTARG=pg_catalog.int4,RIGHTARG=pg_catalog.int4,FUNCTION=public.recipient_int_equal);
   CREATE FUNCTION public.recipient_text_equal(pg_catalog.text,pg_catalog.text) RETURNS pg_catalog.bool LANGUAGE plpgsql AS $$BEGIN
    INSERT INTO public.recipient_search_path_calls VALUES(current_user,'text operator');RETURN $1 OPERATOR(pg_catalog.=) $2;END;$$;
   CREATE OPERATOR public.= (LEFTARG=pg_catalog.text,RIGHTARG=pg_catalog.text,FUNCTION=public.recipient_text_equal);
   RESET ROLE;
   SET search_path=public,pg_catalog`)
		// Positive control: the previous unqualified lookup really invokes
		// application-owned code under the original privileged caller's identity.
		recipientExec(t, f.root, `SELECT pg_catalog.pg_advisory_lock(424242::pg_catalog.int8)`)
		recipientExec(t, f.root, `SELECT session_user::text,current_user::text,EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=pg_catalog.pg_backend_pid() AND locktype='advisory')`)
		var integerCalls, domainCalls, textCalls int
		if err := f.root.QueryRow(t.Context(), `SELECT pg_catalog.count(*) FILTER(WHERE kind OPERATOR(pg_catalog.=) 'integer operator'::pg_catalog.text),pg_catalog.count(*) FILTER(WHERE kind OPERATOR(pg_catalog.=) 'domain'::pg_catalog.text),pg_catalog.count(*) FILTER(WHERE kind OPERATOR(pg_catalog.=) 'text operator'::pg_catalog.text) FROM public.recipient_search_path_calls WHERE actor OPERATOR(pg_catalog.=) current_user`).Scan(&integerCalls, &domainCalls, &textCalls); err != nil {
			t.Fatal(err)
		}
		if integerCalls == 0 || domainCalls == 0 || textCalls == 0 {
			t.Fatalf("hostile lookup control not effective: int=%d domain=%d text=%d", integerCalls, domainCalls, textCalls)
		}
		recipientExec(t, f.root, `SELECT pg_catalog.pg_advisory_unlock(424242::pg_catalog.int8)`)
		recipientExec(t, f.root, `TRUNCATE public.recipient_search_path_calls`)
		r, e := WithGuardianPolicyWriter(t.Context(), f.root, recipientWriterOptions(), func(ctx context.Context, c *pgx.Conn) error {
			// The callback is allowed to change search_path; both entry and exit
			// fixed-catalog inspections must remain independent of that setting.
			_, err := c.Exec(ctx, `RESET search_path; SET search_path=public,pg_catalog`)
			return err
		})
		if e != nil || !r.OperationCompleted || r.Closed {
			t.Fatalf("qualified writer failed %+v %v", r, e)
		}
		poolConfig, err := pgxpool.ParseConfig(f.raw)
		if err != nil {
			t.Fatal(err)
		}
		poolConfig.MaxConns = 1
		poolConfig.ConnConfig.RuntimeParams["search_path"] = "public,pg_catalog"
		pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		published, err := WithRecipientPublicationTx(t.Context(), pool, recipientTestOptions(), func(context.Context, dbgen.DBTX) error { return nil })
		if err != nil || published.Outcome != SessionTxCommitted || published.Discarded {
			t.Fatalf("qualified publication failed %+v %v", published, err)
		}
		var calls int
		if err = f.root.QueryRow(t.Context(), `SELECT pg_catalog.count(*) FROM public.recipient_search_path_calls`).Scan(&calls); err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Fatalf("fixed-catalog helper executed %d application-owned expressions", calls)
		}
		recipientExec(t, f.root, `RESET search_path;
   DROP OPERATOR public.= (pg_catalog.int4,pg_catalog.int4);
   DROP OPERATOR public.= (pg_catalog.text,pg_catalog.text);
   DROP DOMAIN public.text;
   DROP FUNCTION public.recipient_type_canary();
   DROP FUNCTION public.recipient_int_equal(pg_catalog.int4,pg_catalog.int4);
   DROP FUNCTION public.recipient_text_equal(pg_catalog.text,pg_catalog.text);
   DROP TABLE public.recipient_search_path_calls;
   REVOKE CREATE ON SCHEMA public FROM recipient_web`)
	})
	t.Run("original_writer_identity_cleanup_and_nested_refusal", func(t *testing.T) {
		original := f.original(t)
		r, e := WithGuardianPolicyWriter(t.Context(), original, recipientWriterOptions(), func(ctx context.Context, c *pgx.Conn) error {
			if c != original {
				return errors.New("original connection substituted")
			}
			_, err := c.Exec(ctx, `SET ROLE recipient_alt; RESET ROLE`)
			return err
		})
		if e != nil || !r.OperationCompleted || r.Closed {
			t.Fatalf("legitimate writer failed %+v %v", r, e)
		}
		r, e = WithGuardianPolicyWriter(t.Context(), original, recipientWriterOptions(), func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, `SET ROLE recipient_alt`)
			return err
		})
		if !r.OperationCompleted || !r.Closed || !errors.Is(e, ErrSessionAdmissionCleanup) {
			t.Fatalf("writer leaked role %+v %v", r, e)
		}
		original = f.original(t)
		r, e = WithGuardianPolicyWriter(t.Context(), original, recipientWriterOptions(), func(ctx context.Context, c *pgx.Conn) error {
			_, err := WithGuardianPolicyWriter(ctx, c, recipientWriterOptions(), func(context.Context, *pgx.Conn) error { t.Error("nested writer executed"); return nil })
			return err
		})
		if e == nil || !r.Closed {
			t.Fatalf("reentrant writer accepted %+v %v", r, e)
		}
	})
}

type recipientTraceSQL struct{}
type recipientFaultTracer struct {
	beforeSQL, afterSQL string
	panicSQL            string
	fired               atomic.Bool
}

func (f *recipientFaultTracer) TraceQueryStart(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "pg_advisory_unlock_shared") && f.panicSQL == "unlock" && f.fired.CompareAndSwap(false, true) {
		panic("cleanup panic")
	}
	match := d.SQL == f.beforeSQL
	if strings.HasPrefix(f.beforeSQL, "unlock:") {
		match = strings.Contains(d.SQL, "pg_advisory_unlock_shared") && len(d.Args) == 1 && d.Args[0] == strings.TrimPrefix(f.beforeSQL, "unlock:")
	}
	if match && f.fired.CompareAndSwap(false, true) {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = c.Close(cleanup)
		cancel()
	}
	return context.WithValue(ctx, recipientTraceSQL{}, d.SQL)
}
func (f *recipientFaultTracer) TraceQueryEnd(ctx context.Context, c *pgx.Conn, d pgx.TraceQueryEndData) {
	if ctx.Value(recipientTraceSQL{}) == f.afterSQL && d.Err == nil && f.fired.CompareAndSwap(false, true) {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = c.Close(cleanup)
		cancel()
	}
}

// A real PostgreSQL protocol proxy drops only a successful server COMMIT
// CommandComplete, after the backend has committed. The client cannot know that
// outcome; the fixture separately proves why zero-row/automatic retry is unsafe.
func recipientDropCommitProxy(t *testing.T, raw string) (string, <-chan struct{}) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	upstream := parsed.Host
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ack := make(chan struct{})
	var once sync.Once
	var conns sync.Map
	t.Cleanup(func() {
		_ = listener.Close()
		conns.Range(func(k, v any) bool { _ = k.(net.Conn).Close(); return true })
	})
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			backend, err := net.DialTimeout("tcp", upstream, time.Second)
			if err != nil {
				_ = client.Close()
				continue
			}
			conns.Store(client, true)
			conns.Store(backend, true)
			go func() {
				defer client.Close()
				defer backend.Close()
				go func() { _, _ = io.Copy(backend, client); _ = backend.Close() }()
				for {
					header := make([]byte, 5)
					if _, err := io.ReadFull(backend, header); err != nil {
						return
					}
					length := int(binary.BigEndian.Uint32(header[1:]))
					if length < 4 || length > 4<<20 {
						return
					}
					payload := make([]byte, length-4)
					if _, err := io.ReadFull(backend, payload); err != nil {
						return
					}
					if header[0] == 'C' && bytes.Equal(payload, []byte("COMMIT\x00")) {
						once.Do(func() { close(ack) })
						return
					}
					if _, err := client.Write(append(header, payload...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	parsed.Host = listener.Addr().String()
	return parsed.String(), ack
}
