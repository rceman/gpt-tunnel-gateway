package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type task struct {
	ID     string `json:"id"`
	SHA    string `json:"sha256"`
	Branch string `json:"branch"`
}
type result struct {
	SchemaVersion    string `json:"schema_version"`
	TaskID           string `json:"task_id"`
	TaskSHA          string `json:"task_sha256"`
	TargetRepository string `json:"target_repository"`
	Branch           string `json:"branch"`
	BaseHEAD         string `json:"base_head"`
	FinalHEAD        string `json:"final_head"`
	FinishedAt       string `json:"finished_at"`
}

func readJSON(path string, out any) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return b, nil
}
func main() {
	if len(os.Args) >= 3 && os.Args[1] == "bootstrap" && os.Args[2] == "finalize" {
		bootstrap(os.Args[3:])
		return
	}
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnel bootstrap finalize --task-file FILE --result-file FILE")
	os.Exit(2)
}
func bootstrap(args []string) {
	fs := flag.NewFlagSet("finalize", flag.ExitOnError)
	tf := fs.String("task-file", "", "task JSON")
	rf := fs.String("result-file", "", "result JSON")
	_ = fs.Parse(args)
	if *tf == "" || *rf == "" {
		fail(errors.New("both task-file and result-file are required"))
	}
	tb, err := readJSON(*tf, &task{})
	if err != nil {
		fail(err)
	}
	var t task
	_, err = readJSON(*tf, &t)
	if err != nil {
		fail(err)
	}
	sum := sha256.Sum256(tb)
	rawSHA := hex.EncodeToString(sum[:])
	if t.ID != "70efa95d-c2fb-4566-9549-e4b785b9741b" {
		fail(errors.New("unexpected task id"))
	}
	if t.SHA == "" {
		fail(errors.New("task sha256 is missing"))
	}
	var r result
	if _, err = readJSON(*rf, &r); err != nil {
		fail(err)
	}
	if r.TaskID != t.ID || r.TaskSHA != t.SHA || r.TargetRepository != "rceman/gpt-tunnel-gateway" || r.Branch != t.Branch || r.FinalHEAD == "" {
		fail(errors.New("result does not match bootstrap task"))
	}
	if r.FinishedAt == "" {
		fail(errors.New("finished_at is required"))
	}
	_ = rawSHA // task sha is the signed task field; raw bytes are intentionally not substituted.
	cmd := exec.Command("git", "ls-remote", "origin", "refs/heads/"+t.Branch)
	out, err := cmd.Output()
	if err != nil {
		fail(fmt.Errorf("remote verification: %w", err))
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 || fields[0] != r.FinalHEAD {
		fail(fmt.Errorf("remote HEAD %q does not equal result %q", strings.TrimSpace(string(out)), r.FinalHEAD))
	}
	final := filepath.Join(filepath.Dir(*rf), "finalized.json")
	tmp := final + ".tmp-" + fmt.Sprint(time.Now().UnixNano())
	payload := map[string]any{"status": "finalized", "task_id": r.TaskID, "task_sha256": r.TaskSHA, "branch": r.Branch, "final_head": r.FinalHEAD, "finalized_at": time.Now().UTC().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(payload, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		fail(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		fail(err)
	}
	fmt.Println("BOOTSTRAP_TASK_FINALIZED")
}
func fail(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnel:", err); os.Exit(1) }
