package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/scheduler"
)

type schedulerRunner interface {
	Register(scheduler.JobSpec) error
	Run(context.Context) error
}

func serverSchedulerLoops(pool *pgxpool.Pool, jobs []scheduler.JobSpec) ([]schedulerRunner, error) {
	return registerSchedulerLoops(func() schedulerRunner {
		return scheduler.NewManager(pool, scheduler.Options{})
	}, jobs)
}

func registerSchedulerLoops(newManager func() schedulerRunner, jobs []scheduler.JobSpec) ([]schedulerRunner, error) {
	// Each delivery path has its own sequential loop: maintenance cannot
	// delay either, and child-rule backfill cannot delay wakeup dispatch.
	// Keep the job names unchanged because they identify distributed leases.
	maintenance, wakeups, childEvents := newManager(), newManager(), newManager()
	for _, job := range jobs {
		manager := maintenance
		switch job.Name {
		case "issue_wakeup_dispatch":
			manager = wakeups
		case "issue_child_event_sweep":
			manager = childEvents
		}
		if err := manager.Register(job); err != nil {
			return nil, fmt.Errorf("register scheduler job %q: %w", job.Name, err)
		}
	}
	return []schedulerRunner{maintenance, wakeups, childEvents}, nil
}

// runSchedulerLoops starts one long-lived goroutine per manager, never per tick.
// Completion is joined during shutdown before the database pool is closed.
func runSchedulerLoops(ctx context.Context, loops []schedulerRunner) <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, loop := range loops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = loop.Run(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}
