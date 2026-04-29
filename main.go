package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type RPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Job struct {
	ID        string    `json:"id"`
	Command   []string  `json:"command"`
	CWD       string    `json:"cwd"`
	PID       int       `json:"pid,omitempty"`
	Status    string    `json:"status"` // running, exited, failed, canceled
	ExitCode  *int      `json:"exit_code,omitempty"`
	LogPath   string    `json:"log_path"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type Server struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	baseDir string
}

func main() {
	baseDir := filepath.Join(os.Getenv("HOME"), ".claude-longjob-mcp")
	_ = os.MkdirAll(filepath.Join(baseDir, "jobs"), 0755)
	_ = os.MkdirAll(filepath.Join(baseDir, "logs"), 0755)

	s := &Server{
		jobs:    map[string]*Job{},
		baseDir: baseDir,
	}
	s.loadJobs()

	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		resp := s.handle(req)
		if resp == nil {
			continue
		}

		b, _ := json.Marshal(resp)
		_, _ = writer.Write(b)
		_, _ = writer.WriteString("\n")
		_ = writer.Flush()
	}
}

func (s *Server) handle(req RPCRequest) *RPCResponse {
	switch req.Method {
	case "initialize":
		return ok(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "claude-longjob-mcp",
				"version": "0.1.0",
			},
		})

	case "notifications/initialized":
		return nil

	case "tools/list":
		return ok(req.ID, map[string]any{
			"tools": []any{
				tool("start_long_job", "Start a long-running command in the background and return immediately.", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "array",
							"description": "Command and args, e.g. [\"cargo\", \"build\"]",
							"items":       map[string]any{"type": "string"},
						},
						"cwd": map[string]any{
							"type":        "string",
							"description": "Working directory",
						},
						"instruction": map[string]any{
							"type":        "string",
							"description": "Instruction to include in completion notification",
						},
					},
					"required": []string{"command", "cwd"},
				}),
				tool("list_jobs", "List known long-running jobs.", map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}),
				tool("get_job", "Get one job status.", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id": map[string]any{"type": "string"},
					},
					"required": []string{"job_id"},
				}),
				tool("tail_job_log", "Read the tail of a job log.", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id": map[string]any{"type": "string"},
						"lines":  map[string]any{"type": "number"},
					},
					"required": []string{"job_id"},
				}),
				tool("cancel_job", "Cancel a running job with SIGTERM.", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"job_id": map[string]any{"type": "string"},
					},
					"required": []string{"job_id"},
				}),
			},
		})

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(req.ID, -32602, err.Error())
		}

		result, err := s.callTool(p.Name, p.Arguments)
		if err != nil {
			return ok(req.ID, textResult("ERROR: "+err.Error()))
		}
		return ok(req.ID, textResult(result))

	default:
		return fail(req.ID, -32601, "method not found")
	}
}

func (s *Server) callTool(name string, args json.RawMessage) (string, error) {
	switch name {
	case "start_long_job":
		var a struct {
			Command     []string `json:"command"`
			CWD         string   `json:"cwd"`
			Instruction string   `json:"instruction"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return s.startLongJob(a.Command, a.CWD, a.Instruction)

	case "list_jobs":
		s.mu.Lock()
		defer s.mu.Unlock()
		b, _ := json.MarshalIndent(s.jobs, "", "  ")
		return string(b), nil

	case "get_job":
		var a struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(args, &a)
		job, err := s.getJob(a.JobID)
		if err != nil {
			return "", err
		}
		b, _ := json.MarshalIndent(job, "", "  ")
		return string(b), nil

	case "tail_job_log":
		var a struct {
			JobID string `json:"job_id"`
			Lines int    `json:"lines"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Lines <= 0 {
			a.Lines = 120
		}
		return s.tailJobLog(a.JobID, a.Lines)

	case "cancel_job":
		var a struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(args, &a)
		return s.cancelJob(a.JobID)

	default:
		return "", errors.New("unknown tool: " + name)
	}
}

func (s *Server) startLongJob(command []string, cwd, instruction string) (string, error) {
	if len(command) == 0 {
		return "", errors.New("command is required")
	}
	if cwd == "" {
		return "", errors.New("cwd is required")
	}

	id := "job_" + time.Now().Format("20060102_150405")
	logDir := filepath.Join(s.baseDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", err
	}
	logPath := filepath.Join(logDir, id+".log")

	logFile, err := os.Create(logPath)
	if err != nil {
		return "", err
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = cwd
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", err
	}

	job := &Job{
		ID:        id,
		Command:   command,
		CWD:       cwd,
		PID:       cmd.Process.Pid,
		Status:    "running",
		LogPath:   logPath,
		StartedAt: time.Now(),
	}

	s.mu.Lock()
	s.jobs[id] = job
	s.mu.Unlock()
	s.saveJob(job)

	go func() {
		err := cmd.Wait()
		_ = logFile.Close()

		exitCode := 0
		status := "exited"
		errText := ""

		if err != nil {
			status = "failed"
			errText = err.Error()
			if ee, ok := err.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else {
				exitCode = -1
			}
		}

		s.mu.Lock()
		job.Status = status
		job.ExitCode = &exitCode
		job.EndedAt = time.Now()
		job.Error = errText
		s.mu.Unlock()

		s.saveJob(job)
		s.notifyJobFinished(job, instruction)
	}()

	b, _ := json.MarshalIndent(job, "", "  ")
	return string(b), nil
}

func (s *Server) getJob(id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[id]
	if !ok {
		return nil, errors.New("job not found: " + id)
	}
	return job, nil
}

func (s *Server) tailJobLog(id string, lines int) (string, error) {
	job, err := s.getJob(id)
	if err != nil {
		return "", err
	}

	f, err := os.Open(job.LogPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	all, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}

	parts := strings.Split(string(all), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}

func (s *Server) cancelJob(id string) (string, error) {
	job, err := s.getJob(id)
	if err != nil {
		return "", err
	}
	if job.Status != "running" {
		return "job is not running", nil
	}

	// Kill process group so child processes also receive SIGTERM.
	err = syscall.Kill(-job.PID, syscall.SIGTERM)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	job.Status = "canceled"
	job.EndedAt = time.Now()
	s.mu.Unlock()

	s.saveJob(job)
	return "sent SIGTERM to job " + id, nil
}

func (s *Server) notifyJobFinished(job *Job, instruction string) {
	payload := map[string]string{
		"event":       "job.finished",
		"job_id":      job.ID,
		"status":      job.Status,
		"exit_code":   intPtrToString(job.ExitCode),
		"log_path":    job.LogPath,
		"cwd":         job.CWD,
		"instruction": instruction,
	}

	// Minimal adapter point:
	// LONGJOB_NOTIFY_COMMAND='curl -X POST http://127.0.0.1:8787/events ...'
	notifyCmd := os.Getenv("LONGJOB_NOTIFY_COMMAND")
	if notifyCmd == "" {
		return
	}

	b, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-lc", notifyCmd)
	cmd.Env = append(os.Environ(),
		"LONGJOB_EVENT_JSON="+string(b),
		"LONGJOB_JOB_ID="+job.ID,
		"LONGJOB_STATUS="+job.Status,
		"LONGJOB_EXIT_CODE="+intPtrToString(job.ExitCode),
		"LONGJOB_LOG_PATH="+job.LogPath,
		"LONGJOB_INSTRUCTION="+instruction,
	)
	_ = cmd.Run()
}

func (s *Server) saveJob(job *Job) {
	jobDir := filepath.Join(s.baseDir, "jobs")
	_ = os.MkdirAll(jobDir, 0755)
	path := filepath.Join(jobDir, job.ID+".json")
	b, _ := json.MarshalIndent(job, "", "  ")
	_ = os.WriteFile(path, b, 0644)
}

func (s *Server) loadJobs() {
	dir := filepath.Join(s.baseDir, "jobs")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var job Job
		if json.Unmarshal(b, &job) == nil {
			s.jobs[job.ID] = &job
		}
	}
}

func ok(id any, result any) *RPCResponse {
	return &RPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func fail(id any, code int, msg string) *RPCResponse {
	return &RPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func tool(name, description string, schema map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": schema,
	}
}

func textResult(s string) map[string]any {
	return map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": s,
			},
		},
	}
}

func intPtrToString(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
