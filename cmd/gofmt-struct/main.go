package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/gofmtstruct"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("gofmt-struct", flag.ContinueOnError)
	flags.SetOutput(errOut)
	check := flags.Bool("check", false, "check canonical structural formatting without changing files")
	write := flags.Bool("write", false, "rewrite files using canonical structural formatting")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *check == *write {
		return errors.New("exactly one of --check or --write is required")
	}
	paths := flags.Args()
	if len(paths) == 0 {
		return errors.New("at least one Go file or directory is required")
	}
	files, err := goFiles(paths)
	if err != nil {
		return err
	}
	changed := make([]string, 0)
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		canonical, err := gofmtstruct.FormatSource(path, source)
		if err != nil {
			return fmt.Errorf("format %s: %w", path, err)
		}
		if bytes.Equal(source, canonical) {
			continue
		}
		changed = append(changed, path)
		if *write {
			if err := os.WriteFile(path, canonical, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			continue
		}
	}
	if len(changed) > 0 && *check {
		for _, path := range changed {
			fmt.Fprintln(out, path)
		}
		return fmt.Errorf("%d file(s) need canonical keyed-struct formatting", len(changed))
	}
	return nil
}

func goFiles(paths []string) ([]string, error) {
	files := make([]string, 0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if filepath.Ext(path) == ".go" {
				files = append(files, path)
			}
			continue
		}
		err = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			if !entry.IsDir() && filepath.Ext(current) == ".go" {
				files = append(files, current)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}
