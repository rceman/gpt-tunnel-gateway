package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var version = "0.6.11"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	if os.Args[1] == "version" || os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if os.Args[1] == "--source-sha" {
		fmt.Println(releaseartifacts.BuildSourceRevision)
		return
	}
	c, err := config.Load("")
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	group := os.Args[1]
	args := os.Args[2:]
	var s *service.Service
	if group == "prompt" {
		db, openErr := sqlitestore.Open(c.StateDir)
		if openErr != nil {
			fatal(openErr)
		}
		defer db.Close()
		s = service.NewWithDurability(c, db)
	} else {
		s = service.New(c)
	}
	switch group {
	case "format", "check", "test":
		gate(ctx, s, group, args)
	case "verify":
		verify(ctx, s, args)
	case "work":
		work(ctx, s, args)
	case "project":
		project(ctx, s, args)
	case "plan":
		plan(ctx, s, args)
	case "adr":
		adr(ctx, s, args)
	case "task":
		task(ctx, s, args)
	case "agent":
		agent(ctx, s, args)
	case "prompt":
		prompt(ctx, s, args)
	case "watcher":
		watcher(ctx, s, args)
	case "operator":
		operator(ctx, s, args)
	case "git":
		gitcmd(ctx, s, args)
	case "query":
		query(ctx, s, args)
	default:
		usage()
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnel {format|check|test|verify|work|project|plan|adr|task|agent|prompt|watcher|operator|git|query} [args]")
	fmt.Fprintln(os.Stderr, "new operational IDs: CODE-TSK<N>, CODE-TSK<N>-RUN<M>, CODE-ADR<N>, CODE-OPR<N>")
	fmt.Fprintln(os.Stderr, "task create --file requires slug; branch and base_revision are derived by the gateway")
	fmt.Fprintln(os.Stderr, "pre-cutover IDs remain read-only history and are not accepted by operational mutations")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnel:", err); os.Exit(1) }
func output(v any) {
	data, err := json.MarshalIndent(normalizeCLITimestamps(v), "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(data))
}

var cliTimestampType = reflect.TypeOf(time.Time{})

func normalizeCLITimestamps(v any) any {
	value := normalizeCLIValue(reflect.ValueOf(v))
	if !value.IsValid() {
		return nil
	}
	return value.Interface()
}

func normalizeCLIValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	if value.Type() == cliTimestampType {
		return reflect.ValueOf(value.Interface().(time.Time).UTC().Truncate(time.Second))
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		return normalizeCLIValue(value.Elem())
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(normalizeCLIValue(value.Elem()))
		return result
	case reflect.Struct:
		result := reflect.New(value.Type()).Elem()
		result.Set(value)
		for i := 0; i < value.NumField(); i++ {
			if !result.Field(i).CanSet() {
				continue
			}
			normalized := normalizeCLIValue(value.Field(i))
			if normalized.IsValid() && normalized.Type().AssignableTo(result.Field(i).Type()) {
				result.Field(i).Set(normalized)
			}
		}
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(normalizeCLIValue(value.Index(i)))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			result.Index(i).Set(normalizeCLIValue(value.Index(i)))
		}
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result.SetMapIndex(iter.Key(), normalizeCLIValue(iter.Value()))
		}
		return result
	default:
		return value
	}
}
func readFile(path string, out any) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		fatal(fmt.Errorf("trailing JSON content"))
	}
}
func fileFlag(name string, args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1], append(args[:i], args[i+2:]...)
		}
	}
	return "", args
}
func expected(args []string) (string, []string) { return fileFlag("--expected-hub-revision", args) }
func expectedStrict(args []string) (string, error) {
	expectedRevision, rest := expected(args)
	if len(rest) != 0 {
		return "", fmt.Errorf("unexpected run cancellation acknowledgement arguments")
	}
	return expectedRevision, nil
}
func require(args []string, n int) {
	if len(args) < n {
		usage()
	}
}
