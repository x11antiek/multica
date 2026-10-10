package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func waitFinalization(t *testing.T, done <-chan struct{}) bool {
	t.Helper()
	select {
	case <-done:
		return true
	case <-time.After(time.Second):
		return false
	}
}

func TestManagerFinalizationTimeout(t *testing.T) {
	for _, handlerErr := range []error{nil, errors.New("controlled failure")} {
		name := "success"
		if handlerErr != nil {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			pool := integrationPool(t)
			job := newTestJobSpec(uniqueJobName(t, "finalization_timeout"))
			t.Cleanup(func() { cleanupExecutions(t, pool, job.Name) })
			ctx := context.Background()
			now, err := dbNow(ctx, pool)
			if err != nil {
				t.Fatal(err)
			}
			plan := FloorPlan(now, job.Cadence)
			c, err := tryClaim(ctx, pool, job, ScopeGlobal, plan, now, "bounded-finalizer")
			if err != nil || !c.Won {
				t.Fatalf("claim: %+v %v", c, err)
			}
			// Hold the exact execution row. The terminal UPDATE must block at the
			// database boundary rather than relying on sleeps to delay a mock.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT id FROM sys_cron_executions WHERE id=$1 FOR UPDATE", c.ID); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			job.Handler = func(context.Context, HandlerInput) (HandlerResult, error) {
				close(entered)
				return HandlerResult{}, handlerErr
			}
			mgr := NewManager(pool, Options{FinalizationTimeout: 50 * time.Millisecond})
			done := make(chan struct{})
			go func() { mgr.runClaimed(ctx, job, ScopeGlobal, plan, c); close(done) }()
			<-entered
			finished := waitFinalization(t, done)
			var status string
			if finished {
				// Observe while the lock is still held. After releasing it, a
				// concurrent PostgreSQL cancel request can race an UPDATE that
				// was already sent; a client timeout cannot promise rollback.
				if err := pool.QueryRow(ctx, "SELECT status FROM sys_cron_executions WHERE id=$1", c.ID).Scan(&status); err != nil {
					t.Fatal(err)
				}
			}
			// Release even when the old unbounded implementation fails, so the
			// regression never leaves a goroutine or lock behind.
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if !finished {
				if !waitFinalization(t, done) {
					t.Fatal("finalizer did not exit after lock release")
				}
				t.Fatal("terminal lease update exceeded its detached timeout while execution row was locked")
			}
			if status != "RUNNING" {
				t.Fatalf("timed-out finalization wrote status %q", status)
			}
		})
	}
}

func TestManagerFinalizationClockReadTimeout(t *testing.T) {
	pool := integrationPool(t)
	config := pool.Config()
	config.MaxConns = 1
	limitedPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer limitedPool.Close()
	// Saturation blocks both the detached clock read and terminal write.
	conn, err := limitedPool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	entered := make(chan struct{})
	job := newTestJobSpec("saturated-finalization")
	job.Handler = func(context.Context, HandlerInput) (HandlerResult, error) {
		close(entered)
		return HandlerResult{}, nil
	}
	mgr := NewManager(limitedPool, Options{FinalizationTimeout: 50 * time.Millisecond})
	done := make(chan struct{})
	go func() {
		mgr.runClaimed(context.Background(), job, ScopeGlobal, time.Now(), claim{Attempt: 1})
		close(done)
	}()
	<-entered
	finished := waitFinalization(t, done)
	conn.Release()
	if !finished {
		if !waitFinalization(t, done) {
			t.Fatal("finalizer did not exit after connection release")
		}
		t.Fatal("database clock read exceeded the detached finalization timeout")
	}
}

func TestManagerFinalizationSurvivesParentCancellation(t *testing.T) {
	pool := integrationPool(t)
	job := newTestJobSpec(uniqueJobName(t, "cancel_finalization"))
	t.Cleanup(func() { cleanupExecutions(t, pool, job.Name) })
	now, err := dbNow(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	plan := FloorPlan(now, job.Cadence)
	c, err := tryClaim(context.Background(), pool, job, ScopeGlobal, plan, now, "cancel-finalizer")
	if err != nil || !c.Won {
		t.Fatalf("claim: %+v %v", c, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	job.Handler = func(ctx context.Context, _ HandlerInput) (HandlerResult, error) { return HandlerResult{}, ctx.Err() }
	mgr := NewManager(pool, Options{FinalizationTimeout: time.Second})
	mgr.runClaimed(ctx, job, ScopeGlobal, plan, c)
	var status, errorCode string
	if err := pool.QueryRow(context.Background(), "SELECT status, error_code FROM sys_cron_executions WHERE id=$1", c.ID).Scan(&status, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "FAILED" || errorCode != "canceled" {
		t.Fatalf("canceled handler audit = %s/%s, want FAILED/canceled", status, errorCode)
	}
}

func TestManagerRunSerializesHandlerAndJoinsCancellation(t *testing.T) {
	pool := integrationPool(t)
	job := newTestJobSpec(uniqueJobName(t, "serial_loop"))
	job.Cadence = time.Millisecond
	job.HeartbeatInterval = time.Hour
	job.StaleTimeout = 2 * time.Hour
	t.Cleanup(func() { cleanupExecutions(t, pool, job.Name) })
	entered := make(chan int32, 2)
	firstRelease := make(chan struct{})
	var calls, active, overlap atomic.Int32
	job.Handler = func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
		if active.Add(1) != 1 {
			overlap.Add(1)
		}
		defer active.Add(-1)
		call := calls.Add(1)
		entered <- call
		if call == 1 {
			<-firstRelease
		} else {
			<-ctx.Done()
		}
		return HandlerResult{}, ctx.Err()
	}
	mgr := NewManager(pool, Options{TickInterval: time.Millisecond, FinalizationTimeout: time.Second})
	if err := mgr.Register(*job); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = mgr.Run(ctx); close(done) }()
	released := false
	t.Cleanup(func() {
		cancel()
		if !released {
			close(firstRelease)
		}
		if !waitFinalization(t, done) {
			t.Error("manager failed to join cancellation")
		}
	})
	select {
	case call := <-entered:
		if call != 1 {
			t.Fatalf("first handler call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("first handler did not enter")
	}
	close(firstRelease)
	released = true
	select {
	case call := <-entered:
		if call != 2 {
			t.Fatalf("next handler call = %d", call)
		}
	case <-time.After(time.Second):
		t.Fatal("next periodic handler did not enter after release")
	}
	cancel()
	if !waitFinalization(t, done) {
		t.Fatal("manager did not finish cancelled handler and terminal write")
	}
	if calls.Load() != 2 || active.Load() != 0 || overlap.Load() != 0 {
		t.Fatalf("handler calls=%d active=%d overlaps=%d", calls.Load(), active.Load(), overlap.Load())
	}
}
