package inbound

type SchedulerUseCases interface {
	RunScheduledTasks()
	ForceRun(event string)
	HasJob(event string) bool
}
