package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func upgradeResultShouldPrint(status string) bool {
	return status == "UPGRADE_ROLLED_BACK" || status == "UPGRADE_ROLLBACK_FAILED"
}
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", src)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".install-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnelctl {install|init-config|upgrade [inspect|status]|doctor|diagnose-startup|state {check|repair --dry-run|repair --apply|migrate-train-v2-attempts --project <project> --train <train> --dry-run|migrate-train-v2-attempts --project <project> --train <train> --apply|migrate-train-v2-legacy --project <project> --action action:train:sha[:opsha[:mutation:mutationsha]] --dry-run|migrate-train-v2-legacy --project <project> --action action:train:sha[:opsha[:mutation:mutationsha]] --apply --expected-hub-revision <sha>}|logs [gateway|tunnel|all] [lines]|version}")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnelctl:", err); os.Exit(1) }
func output(v any)    { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
