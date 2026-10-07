package service

import (
	"log/slog"
	"time"
)

// scheduledJob is implemented by each job struct.
type scheduledJob interface {
	run(force bool)
	name() string
}

// jobOutcomeReporter is opt-in for jobs that can classify their own business
// result. Never infer business success from a void run returning normally.
type jobOutcomeReporter interface {
	runWithOutcome(force bool) string
}

type SchedulerObserver interface {
	Observe(job, outcome string, at time.Time)
}

// Scheduler orchestrates the registered scheduled jobs.
type Scheduler struct {
	jobs     []scheduledJob
	logger   *slog.Logger
	observer SchedulerObserver
}

// NewScheduler creates a Scheduler with the given jobs executed in the provided order.
func NewScheduler(logger *slog.Logger, jobs ...scheduledJob) *Scheduler {
	return &Scheduler{jobs: jobs, logger: logger}
}

func (s *Scheduler) SetObserver(observer SchedulerObserver) { s.observer = observer }

// RunScheduledTasks is called by the single poll cron (default every 5 minutes).
// It dispatches to all registered jobs in order.
func (s *Scheduler) RunScheduledTasks() {
	for _, j := range s.jobs {
		outcome := "unclassified"
		if reporting, ok := j.(jobOutcomeReporter); ok {
			outcome = reporting.runWithOutcome(false)
		} else {
			j.run(false)
		}
		if s.observer != nil {
			s.observer.Observe(j.name(), outcome, time.Now())
		}
	}
}
