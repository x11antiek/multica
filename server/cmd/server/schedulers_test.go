package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/scheduler"
)

type controlledScheduler struct {
	jobs []scheduler.JobSpec
}

func (s *controlledScheduler) Register(job scheduler.JobSpec) error {
	s.jobs = append(s.jobs, job)
	return nil
}

func (s *controlledScheduler) Run(ctx context.Context) error {
	for _, job := range s.jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, _ = job.Handler(ctx, scheduler.HandlerInput{})
	}
	<-ctx.Done()
	return ctx.Err()
}

type signaledIssueDispatcher struct {
	wakeup       chan struct{}
	child        chan struct{}
	childRelease <-chan struct{}
}

func (d signaledIssueDispatcher) Tick(context.Context) error {
	close(d.wakeup)
	return nil
}

func (d signaledIssueDispatcher) SweepChildEvents(ctx context.Context) error {
	close(d.child)
	if d.childRelease != nil {
		select {
		case <-d.childRelease:
		case <-ctx.Done():
		}
	}
	return nil
}

func (d signaledIssueDispatcher) BackfillChildDoneRules(context.Context, int32) (int, error) {
	return 0, nil
}

func awaitSchedulerSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("did not receive %s while unrelated work was blocked", name)
	}
}

func TestServerSchedulerDeliveryIndependentOfMaintenance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	maintenanceEntered := make(chan struct{})
	maintenanceRelease := make(chan struct{})
	dispatcher := signaledIssueDispatcher{wakeup: make(chan struct{}), child: make(chan struct{})}
	maintenance := scheduler.JobSpec{Name: "blocked_maintenance", Handler: func(context.Context, scheduler.HandlerInput) (scheduler.HandlerResult, error) {
		close(maintenanceEntered)
		<-maintenanceRelease
		return scheduler.HandlerResult{}, nil
	}}
	jobs := []scheduler.JobSpec{maintenance, scheduler.IssueWakeupJob(dispatcher), scheduler.ChildEventSweepJob(dispatcher)}
	loops, err := registerSchedulerLoops(func() schedulerRunner { return &controlledScheduler{} }, jobs)
	if err != nil {
		t.Fatal(err)
	}
	done := runSchedulerLoops(ctx, loops)
	t.Cleanup(func() { cancel(); close(maintenanceRelease); awaitSchedulerSignal(t, done, "scheduler shutdown") })
	awaitSchedulerSignal(t, maintenanceEntered, "maintenance entry")
	awaitSchedulerSignal(t, dispatcher.wakeup, "wakeup dispatch")
	awaitSchedulerSignal(t, dispatcher.child, "child-event dispatch")
	if len(loops) != 3 {
		t.Fatalf("manager count = %d, want exactly three fixed loops", len(loops))
	}
	cancel()
	select {
	case <-done:
		t.Fatal("scheduler shutdown returned before the blocked manager exited")
	default:
	}
}

func TestServerSchedulerWakeupIndependentOfChildSweep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	childRelease := make(chan struct{})
	dispatcher := signaledIssueDispatcher{wakeup: make(chan struct{}), child: make(chan struct{}), childRelease: childRelease}
	// Place child sweep first so combining the two jobs in one loop fails.
	loops, err := registerSchedulerLoops(func() schedulerRunner { return &controlledScheduler{} }, []scheduler.JobSpec{
		scheduler.ChildEventSweepJob(dispatcher), scheduler.IssueWakeupJob(dispatcher),
	})
	if err != nil {
		t.Fatal(err)
	}
	done := runSchedulerLoops(ctx, loops)
	t.Cleanup(func() { cancel(); close(childRelease); awaitSchedulerSignal(t, done, "scheduler shutdown") })
	awaitSchedulerSignal(t, dispatcher.child, "child sweep entry")
	awaitSchedulerSignal(t, dispatcher.wakeup, "wakeup dispatch")
}

type rejectingScheduler struct{ controlledScheduler }

func (*rejectingScheduler) Register(scheduler.JobSpec) error { return errors.New("invalid job") }

func TestServerSchedulerRegistrationFailureDoesNotStartLoops(t *testing.T) {
	loops, err := registerSchedulerLoops(func() schedulerRunner { return &rejectingScheduler{} }, []scheduler.JobSpec{{Name: "invalid"}})
	if err == nil || loops != nil {
		t.Fatalf("registration failure returned loops=%v err=%v", loops, err)
	}
}

// Exercise the same constructor and real DB-backed managers used by main,
// while partitioning the canonical lease names by a test-owned scope.
func TestServerSchedulerDatabaseDeliveryWhileMaintenanceBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := scheduler.Scope{Kind: "workspace", ID: uuid.NewString()}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), "DELETE FROM sys_cron_executions WHERE scope_kind=$1 AND scope_id=$2", scope.Kind, scope.ID); err != nil {
			t.Fatal(err)
		}
	})
	maintenanceEntered := make(chan struct{})
	maintenanceRelease := make(chan struct{})
	dispatcher := signaledIssueDispatcher{wakeup: make(chan struct{}), child: make(chan struct{})}
	maintenance := scheduler.IssueWakeupJob(dispatcher)
	maintenance.Name = "blocked_maintenance_" + uuid.NewString()
	maintenance.Handler = func(context.Context, scheduler.HandlerInput) (scheduler.HandlerResult, error) {
		close(maintenanceEntered)
		<-maintenanceRelease
		return scheduler.HandlerResult{}, nil
	}
	jobs := []scheduler.JobSpec{maintenance, scheduler.IssueWakeupJob(dispatcher), scheduler.ChildEventSweepJob(dispatcher)}
	for i := range jobs {
		jobs[i].Scopes = scheduler.StaticScopes(scope)
	}
	loops, err := serverSchedulerLoops(testPool, jobs)
	if err != nil {
		t.Fatal(err)
	}
	done := runSchedulerLoops(ctx, loops)
	t.Cleanup(func() {
		cancel()
		close(maintenanceRelease)
		awaitSchedulerSignal(t, done, "database scheduler shutdown")
	})
	awaitSchedulerSignal(t, maintenanceEntered, "database maintenance entry")
	awaitSchedulerSignal(t, dispatcher.wakeup, "database wakeup dispatch")
	awaitSchedulerSignal(t, dispatcher.child, "database child-event dispatch")
	var names []string
	rows, err := testPool.Query(context.Background(), "SELECT job_name FROM sys_cron_executions WHERE scope_kind=$1 AND scope_id=$2 ORDER BY job_name", scope.Kind, scope.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 || names[1] != "issue_child_event_sweep" || names[2] != "issue_wakeup_dispatch" {
		t.Fatalf("lease identity changed: %v", names)
	}
}
