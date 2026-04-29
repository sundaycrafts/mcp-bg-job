package main

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"
)

// timestampWriter prepends an RFC3339 timestamp to each line written to the underlying writer.
type timestampWriter struct {
	w       io.Writer
	mu      sync.Mutex
	buf     []byte
	timeNow func() time.Time
}

func newTimestampWriter(w io.Writer) *timestampWriter {
	return &timestampWriter{w: w, timeNow: time.Now}
}

func (tw *timestampWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	n := len(p)
	tw.buf = append(tw.buf, p...)

	for {
		idx := bytes.IndexByte(tw.buf, '\n')
		if idx == -1 {
			break
		}
		line := tw.buf[:idx+1]
		ts := tw.timeNow().UTC().Format(time.RFC3339)
		if _, err := fmt.Fprintf(tw.w, "%s %s", ts, line); err != nil {
			return 0, err
		}
		tw.buf = tw.buf[idx+1:]
	}

	return n, nil
}

// flush writes any remaining buffered bytes (no trailing newline) with a timestamp.
func (tw *timestampWriter) flush() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if len(tw.buf) > 0 {
		ts := tw.timeNow().UTC().Format(time.RFC3339)
		_, err := fmt.Fprintf(tw.w, "%s %s\n", ts, tw.buf)
		tw.buf = nil
		return err
	}
	return nil
}
