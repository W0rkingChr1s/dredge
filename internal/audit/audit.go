// Package audit appends a JSON-lines history of runs.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/W0rkingChr1s/dredge/internal/janitor"
)

// Entry is one line in the history log.
type Entry struct {
	Time     string               `json:"time"`
	Host     string               `json:"host"`
	Mode     string               `json:"mode"` // "auto", "interactive", "dry-run"
	Removed  int                  `json:"removed"`
	FreedMB  int64                `json:"freed_mb"`
	Failed   int                  `json:"failed"`
	Duration string               `json:"duration"`
	Types    []janitor.TypeResult `json:"types"`
}

// Append writes one entry to the log file (creating dirs as needed).
// Failures are returned but callers usually treat logging as best-effort.
func Append(path, host, mode string, res *janitor.Result, dur time.Duration) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	e := Entry{
		Time:     time.Now().Format(time.RFC3339),
		Host:     host,
		Mode:     mode,
		Removed:  res.TotalRemoved(),
		FreedMB:  res.TotalFreed() / (1024 * 1024),
		Failed:   res.TotalFailed(),
		Duration: dur.Round(time.Second).String(),
		Types:    res.Types,
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}
