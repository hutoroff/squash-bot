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

// ForceRun runs the named job bypassing its time-window scheduling gate.
// It is a no-op (with an error log) if the event name is not recognised.
func (s *Scheduler) ForceRun(event string) {
	for _, j := range s.jobs {
		if j.name() == event {
			j.run(true)
			return
		}
	}
	s.logger.Error("ForceRun: unknown event", "event", event)
}

// HasJob reports whether the given event name is handled by a registered job.
func (s *Scheduler) HasJob(event string) bool {
	for _, j := range s.jobs {
		if j.name() == event {
			return true
		}
	}
	return false
}
