// Package gofmtstruct provides the repository's structural Go formatting rule.
package gofmtstruct

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"sort"
)

type edit struct {
	offset int
	text   string
}

// FormatSource rewrites multi-field keyed struct literals vertically and then
// applies gofmt. It does not rewrite maps, arrays, slices, or unkeyed literals.
func FormatSource(filename string, source []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	commas, err := commaOffsets(fset, file.Pos(), source)
	if err != nil {
		return nil, err
	}
	typeKinds := declaredTypeKinds(file)
	edits := make([]edit, 0)
	stack := make([]ast.Node, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		nestedComposite := false
		for _, parent := range stack {
			if _, ok := parent.(*ast.CompositeLit); ok {
				nestedComposite = true
				break
			}
		}
		stack = append(stack, node)
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !isKeyedStructLiteral(literal, typeKinds) || (len(literal.Elts) < 2 && !nestedComposite) {
			return true
		}
		for _, element := range literal.Elts {
			if _, ok := element.(*ast.KeyValueExpr); !ok {
				return true
			}
		}

		open := fset.PositionFor(literal.Lbrace, false).Offset
		close := fset.PositionFor(literal.Rbrace, false).Offset
		first := fset.PositionFor(literal.Elts[0].Pos(), false).Offset
		if !containsNewline(source[open+1 : first]) {
			edits = append(edits, edit{
				offset: open + 1,
				text:   "\n",
			})
		}
		closeHasNewline := false
		for index, element := range literal.Elts {
			end := fset.PositionFor(element.End(), false).Offset
			limit := close
			if index+1 < len(literal.Elts) {
				limit = fset.PositionFor(literal.Elts[index+1].Pos(), false).Offset
			}
			comma := firstCommaAfter(commas, end, limit)
			if comma < 0 {
				if index+1 < len(literal.Elts) {
					return true
				}
				edits = append(edits, edit{
					offset: close,
					text:   ",\n",
				})
				closeHasNewline = true
				continue
			}
			if !containsNewline(source[comma+1 : limit]) {
				edits = append(edits, edit{
					offset: comma + 1,
					text:   "\n",
				})
			}
		}
		if !closeHasNewline && !containsNewline(source[fset.PositionFor(literal.Elts[len(literal.Elts)-1].End(), false).Offset:close]) {
			edits = append(edits, edit{
				offset: close,
				text:   "\n",
			})
		}
		return true
	})

	if len(edits) > 0 {
		sort.SliceStable(edits, func(i, j int) bool { return edits[i].offset < edits[j].offset })
		var transformed bytes.Buffer
		transformed.Grow(len(source) + len(edits))
		cursor := 0
		for _, change := range edits {
			if change.offset < cursor || change.offset > len(source) {
				return nil, fmt.Errorf("invalid formatter edit offset %d", change.offset)
			}
			transformed.Write(source[cursor:change.offset])
			transformed.WriteString(change.text)
			cursor = change.offset
		}
		transformed.Write(source[cursor:])
		source = transformed.Bytes()
	}
	return format.Source(source)
}

func containsNewline(source []byte) bool { return bytes.IndexByte(source, '\n') >= 0 }

func commaOffsets(fset *token.FileSet, first token.Pos, source []byte) ([]int, error) {
	file := fset.File(first)
	if file == nil {
		return nil, fmt.Errorf("formatter token file is unavailable")
	}
	var scan scanner.Scanner
	scan.Init(file, source, nil, scanner.ScanComments)
	commas := make([]int, 0)
	for {
		position, tok, _ := scan.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMA {
			commas = append(commas, file.Offset(position))
		}
	}
	return commas, nil
}

func firstCommaAfter(commas []int, start, limit int) int {
	for _, comma := range commas {
		if comma >= start && comma < limit {
			return comma
		}
	}
	return -1
}

func declaredTypeKinds(file *ast.File) map[string]bool {
	kinds := make(map[string]bool)
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "type" {
			continue
		}
		for _, specification := range gen.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, ok := typeSpec.Type.(*ast.StructType); ok {
				kinds[typeSpec.Name.Name] = true
			} else {
				kinds[typeSpec.Name.Name] = false
			}
		}
	}
	return kinds
}

func isKeyedStructLiteral(literal *ast.CompositeLit, kinds map[string]bool) bool {
	switch literal.Type.(type) {
	case *ast.MapType, *ast.ArrayType:
		return false
	case *ast.StructType:
		return true
	case *ast.Ident:
		kind, known := kinds[literal.Type.(*ast.Ident).Name]
		return !known || kind
	default:
		// A selector expression such as package.Struct is structurally a named
		// type. The explicit map/array forms were excluded above.
		return true
	}
}
