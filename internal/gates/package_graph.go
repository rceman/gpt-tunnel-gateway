package gates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type packageGraphLoader func(context.Context, string) (packageGraph, error)

const packageGraphOutputLimit int64 = 64 << 20

type packageGraphNode struct {
	Target     string
	ImportPath string
	Imports    []string
}

type packageGraph struct {
	nodes   map[string]packageGraphNode
	reverse map[string]map[string]bool
}

type goListPackage struct {
	Dir          string   `json:"Dir"`
	ImportPath   string   `json:"ImportPath"`
	Imports      []string `json:"Imports"`
	TestImports  []string `json:"TestImports"`
	XTestImports []string `json:"XTestImports"`
	ForTest      string   `json:"ForTest"`
	Module       *struct {
		Main bool `json:"Main"`
	} `json:"Module"`
	Error *struct {
		Err string `json:"Err"`
	} `json:"Error"`
}

func loadPackageGraph(ctx context.Context, root string) (packageGraph, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "-test", "-deps", "./...")
	cmd.Dir = root
	outputBuffer := boundedCommandOutput{limit: packageGraphOutputLimit}
	stderrBuffer := boundedCommandOutput{limit: packageGraphOutputLimit}
	cmd.Stdout = &outputBuffer
	cmd.Stderr = &stderrBuffer
	err := cmd.Run()
	if err != nil {
		if outputBuffer.exceeded || stderrBuffer.exceeded {
			return packageGraph{}, fmt.Errorf("go list output exceeds %d bytes", packageGraphOutputLimit)
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return packageGraph{}, fmt.Errorf("go list exited %d: %s", exit.ExitCode(), strings.TrimSpace(stderrBuffer.String()))
		}
		return packageGraph{}, err
	}
	if outputBuffer.exceeded || stderrBuffer.exceeded {
		return packageGraph{}, fmt.Errorf("go list output exceeds %d bytes", packageGraphOutputLimit)
	}
	decoder := json.NewDecoder(bytes.NewReader(outputBuffer.data))
	all := map[string]goListPackage{}
	for {
		var item goListPackage
		err := decoder.Decode(&item)
		if err == io.EOF {
			break
		}
		if err != nil {
			return packageGraph{}, fmt.Errorf("decode go list output: %w", err)
		}
		if item.Error != nil {
			return packageGraph{}, fmt.Errorf("package %q: %s", item.ImportPath, item.Error.Err)
		}
		if item.ImportPath == "" || item.Dir == "" {
			return packageGraph{}, fmt.Errorf("go list returned incomplete package identity")
		}
		if prior, exists := all[item.ImportPath]; exists && prior.Dir != item.Dir {
			return packageGraph{}, fmt.Errorf("ambiguous package %q: %q and %q", item.ImportPath, prior.Dir, item.Dir)
		}
		all[item.ImportPath] = item
	}
	if len(all) == 0 {
		return packageGraph{}, fmt.Errorf("go list returned no packages")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return packageGraph{}, fmt.Errorf("resolve graph root: %w", err)
	}
	graph := packageGraph{
		nodes:   map[string]packageGraphNode{},
		reverse: map[string]map[string]bool{},
	}
	for _, item := range all {
		if item.Module == nil || !item.Module.Main || item.ForTest != "" || strings.HasSuffix(item.ImportPath, ".test") {
			continue
		}
		dir, err := filepath.Abs(item.Dir)
		if err != nil {
			return packageGraph{}, fmt.Errorf("resolve package %q directory: %w", item.ImportPath, err)
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return packageGraph{}, fmt.Errorf("package %q is outside repository root", item.ImportPath)
		}
		target := packageTarget(rel)
		if prior, exists := graph.nodes[target]; exists && prior.ImportPath != item.ImportPath {
			return packageGraph{}, fmt.Errorf("ambiguous package target %q", target)
		}
		imports := append([]string{}, item.Imports...)
		imports = append(imports, item.TestImports...)
		imports = append(imports, item.XTestImports...)
		graph.nodes[target] = packageGraphNode{
			Target:     target,
			ImportPath: item.ImportPath,
			Imports:    imports,
		}
	}
	if len(graph.nodes) == 0 {
		return packageGraph{}, fmt.Errorf("go list returned no repository packages")
	}
	for target, node := range graph.nodes {
		for _, imported := range node.Imports {
			if _, exists := all[imported]; !exists {
				return packageGraph{}, fmt.Errorf("package %q imports unresolved package %q", node.ImportPath, imported)
			}
			for importedTarget, importedNode := range graph.nodes {
				if importedNode.ImportPath != imported {
					continue
				}
				if graph.reverse[importedTarget] == nil {
					graph.reverse[importedTarget] = map[string]bool{}
				}
				graph.reverse[importedTarget][target] = true
			}
		}
	}
	return graph, nil
}

type boundedCommandOutput struct {
	data     []byte
	limit    int64
	exceeded bool
}

func (b *boundedCommandOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - int64(len(b.data))
	if remaining > 0 {
		if int64(n) > remaining {
			p = p[:remaining]
			b.exceeded = true
		}
		b.data = append(b.data, p...)
	} else if n > 0 {
		b.exceeded = true
	}
	return n, nil
}

func (b *boundedCommandOutput) String() string { return string(b.data) }
