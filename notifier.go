package main

type JobEvent struct {
	JobID       string
	Status      string
	ExitCode    *int
	LogPath     string
	CWD         string
	Instruction string
}

type Notifier interface {
	Notify(event JobEvent) error
}
