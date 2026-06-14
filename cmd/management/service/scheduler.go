package service

import "log/slog"

// scheduledJob is implemented by each job struct.
type scheduledJob interface {
	run(force bool)
	name() string
}

// Scheduler orchestrates the registered scheduled jobs.
type Scheduler struct {
	jobs   []scheduledJob
	logger *slog.Logger
}

// NewScheduler creates a Scheduler with the given jobs executed in the provided order.
func NewScheduler(logger *slog.Logger, jobs ...scheduledJob) *Scheduler {
	return &Scheduler{jobs: jobs, logger: logger}
}

// RunScheduledTasks is called by the single poll cron (default every 5 minutes).
// It dispatches to all registered jobs in order.
func (s *Scheduler) RunScheduledTasks() {
	for _, j := range s.jobs {
		j.run(false)
	}
}
