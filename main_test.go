package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	dir := t.TempDir()
	s := &Server{
		jobs:    map[string]*Job{},
		baseDir: dir,
	}
	t.Cleanup(s.wg.Wait)
	return s
}

func waitJobFinished(t *testing.T, s *Server, jobID string) *Job {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		job, err := s.getJob(jobID)
		if err != nil {
			t.Fatal(err)
		}

		if job.Status != "running" {
			return job
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("job did not finish in time: %s", jobID)
	return nil
}

func TestStartLongJobSuccess(t *testing.T) {
	s := newTestServer(t)

	result, err := s.startLongJob(
		[]string{"sh", "-c", "echo hello && echo done"},
		t.TempDir(),
		"test instruction",
	)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "job_") {
		t.Fatalf("expected job result, got: %s", result)
	}

	var jobID string
	for id := range s.jobs {
		jobID = id
	}

	job := waitJobFinished(t, s, jobID)

	if job.Status != "exited" {
		t.Fatalf("expected exited, got %s", job.Status)
	}
	if job.ExitCode == nil || *job.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %+v", job.ExitCode)
	}

	log, err := s.tailJobLog(jobID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "hello") || !strings.Contains(log, "done") {
		t.Fatalf("unexpected log: %s", log)
	}
}

func TestStartLongJobFailure(t *testing.T) {
	s := newTestServer(t)

	_, err := s.startLongJob(
		[]string{"sh", "-c", "echo failing && exit 42"},
		t.TempDir(),
		"",
	)
	if err != nil {
		t.Fatal(err)
	}

	var jobID string
	for id := range s.jobs {
		jobID = id
	}

	job := waitJobFinished(t, s, jobID)

	if job.Status != "failed" {
		t.Fatalf("expected failed, got %s", job.Status)
	}
	if job.ExitCode == nil || *job.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %+v", job.ExitCode)
	}
}

func TestCancelJob(t *testing.T) {
	s := newTestServer(t)

	_, err := s.startLongJob(
		[]string{"sh", "-c", "sleep 30"},
		t.TempDir(),
		"",
	)
	if err != nil {
		t.Fatal(err)
	}

	var jobID string
	for id := range s.jobs {
		jobID = id
	}

	msg, err := s.cancelJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "SIGTERM") {
		t.Fatalf("unexpected cancel message: %s", msg)
	}

	// Wait for the goroutine to finish so the status is final.
	waitJobFinished(t, s, jobID)

	job, err := s.getJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "canceled" {
		t.Fatalf("expected canceled, got %s", job.Status)
	}
}

func TestSaveAndLoadJobs(t *testing.T) {
	dir := t.TempDir()

	s1 := &Server{
		jobs:    map[string]*Job{},
		baseDir: dir,
	}

	job := &Job{
		ID:      "job_test",
		Command: []string{"echo", "hello"},
		CWD:     "/tmp",
		Status:  "exited",
		LogPath: filepath.Join(dir, "logs", "job_test.log"),
	}
	s1.saveJob(job)

	s2 := &Server{
		jobs:    map[string]*Job{},
		baseDir: dir,
	}
	s2.loadJobs()

	loaded, err := s2.getJob("job_test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "exited" {
		t.Fatalf("expected exited, got %s", loaded.Status)
	}
}

func TestNotifyJobFinishedCommand(t *testing.T) {
	s := newTestServer(t)

	out := filepath.Join(t.TempDir(), "notify.log")
	t.Setenv("LONGJOB_NOTIFY_COMMAND", "printf '%s' \"$LONGJOB_EVENT_JSON\" > "+out)

	exitCode := 0
	job := &Job{
		ID:       "job_notify",
		Status:   "exited",
		ExitCode: &exitCode,
		LogPath:  "/tmp/job.log",
		CWD:      "/tmp",
	}

	s.notifyJobFinished(job, "continue next task")

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	got := string(b)
	if !strings.Contains(got, "job.finished") {
		t.Fatalf("expected event payload, got: %s", got)
	}
	if !strings.Contains(got, "continue next task") {
		t.Fatalf("expected instruction, got: %s", got)
	}

	// This only tests the notification-command adapter.
	// Sending a real Claude Code Channel event requires a running Claude Code
	// session and the channel transport, so it should be covered by an
	// integration test/manual test later.
}
