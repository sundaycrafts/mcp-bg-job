package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

type ChannelNotifier struct {
	out *bufio.Writer
	mu  *sync.Mutex
}

func (n *ChannelNotifier) Notify(event JobEvent) error {
	type params struct {
		Content string            `json:"content"`
		Meta    map[string]string `json:"meta"`
	}
	type notification struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  params `json:"params"`
	}

	msg := notification{
		JSONRPC: "2.0",
		Method:  "notifications/claude/channel",
		Params: params{
			Content: buildContent(event),
			Meta:    buildMeta(event),
		},
	}

	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if _, err := fmt.Fprintf(n.out, "%s\n", b); err != nil {
		return err
	}
	return n.out.Flush()
}

func buildContent(event JobEvent) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Job %s finished: %s", event.JobID, event.Status)
	if event.ExitCode != nil {
		fmt.Fprintf(&sb, " (exit code %d)", *event.ExitCode)
	}
	fmt.Fprintf(&sb, "\nLog: %s", event.LogPath)
	if event.Instruction != "" {
		fmt.Fprintf(&sb, "\n\n%s", event.Instruction)
	}
	return sb.String()
}

func buildMeta(event JobEvent) map[string]string {
	meta := map[string]string{
		"job_id": event.JobID,
		"status": event.Status,
	}
	if event.ExitCode != nil {
		meta["exit_code"] = strconv.Itoa(*event.ExitCode)
	}
	return meta
}
