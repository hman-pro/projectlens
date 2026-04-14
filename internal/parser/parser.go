// Package parser extracts symbols from Go source files using go/packages
// for type-checked parsing.
package parser

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Symbol represents a single Go declaration extracted from source.
type Symbol struct {
	Name       string // identifier name
	Kind       string // "func", "method", "struct", "interface", "const", "var"
	Package    string // package name (e.g., "foo")
	Receiver   string // for methods only (e.g., "*MyStruct")
	Signature  string // full signature line
	DocComment string // doc comment text
	Body       string // source body text
	LineStart  int    // first line number
	LineEnd    int    // last line number
	FilePath   string // absolute file path
}

// FileResult holds parsed symbols for a single file.
type FileResult struct {
	Path    string
	Package string
	Symbols []Symbol
}

// ParseResult holds results for all parsed files.
type ParseResult struct {
	Files []FileResult
}

// Parse loads Go packages from dir matching the given patterns and extracts
// symbols from every source file. The patterns argument uses Go package
// patterns (e.g., "./..." to load all packages under dir).
func Parse(ctx context.Context, dir string, patterns []string) (*ParseResult, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("parser: directory %q: %w", dir, err)
	}

	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedDeps,
		Context: ctx,
		Dir:     dir,
		Fset:    token.NewFileSet(),
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("parser: loading packages: %w", err)
	}

	// Check for package-level errors (but not type-checking errors in deps).
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			return nil, fmt.Errorf("parser: package %s: %s", pkg.PkgPath, e)
		}
	}

	result := &ParseResult{}

	// Cache file contents to avoid re-reading.
	fileContents := make(map[string][]byte)
	readFile := func(path string) ([]byte, error) {
		if data, ok := fileContents[path]; ok {
			return data, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fileContents[path] = data
		return data, nil
	}

	for _, pkg := range pkgs {
		for i, file := range pkg.Syntax {
			if i >= len(pkg.GoFiles) {
				break
			}
			filePath := pkg.GoFiles[i]

			src, err := readFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("parser: reading %s: %w", filePath, err)
			}

			fr := FileResult{
				Path:    filePath,
				Package: pkg.Name,
			}

			extractSymbols(cfg.Fset, file, pkg.Name, filePath, src, &fr)
			result.Files = append(result.Files, fr)
		}
	}

	return result, nil
}

// extractSymbols walks the AST of a single file and populates fr.Symbols.
func extractSymbols(fset *token.FileSet, file *ast.File, pkgName, filePath string, src []byte, fr *FileResult) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			sym := extractFunc(fset, decl, pkgName, filePath, src)
			fr.Symbols = append(fr.Symbols, sym)
			return false

		case *ast.GenDecl:
			syms := extractGenDecl(fset, decl, pkgName, filePath, src)
			fr.Symbols = append(fr.Symbols, syms...)
			return false
		}
		return true
	})
}

// extractFunc extracts a Symbol from a function or method declaration.
func extractFunc(fset *token.FileSet, decl *ast.FuncDecl, pkgName, filePath string, src []byte) Symbol {
	sym := Symbol{
		Name:     decl.Name.Name,
		Kind:     "func",
		Package:  pkgName,
		FilePath: filePath,
	}

	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		sym.Kind = "method"
		sym.Receiver = exprString(decl.Recv.List[0].Type)
	}

	if decl.Doc != nil {
		sym.DocComment = decl.Doc.Text()
	}

	// Signature: print the func declaration without the body.
	sym.Signature = funcSignature(fset, decl)

	// Line range.
	startPos := fset.Position(decl.Pos())
	endPos := fset.Position(decl.End())
	sym.LineStart = startPos.Line
	sym.LineEnd = endPos.Line

	// Body: source text of the entire declaration.
	sym.Body = sourceSlice(src, fset, decl.Pos(), decl.End())

	return sym
}

// funcSignature returns the declaration line without the body.
func funcSignature(fset *token.FileSet, decl *ast.FuncDecl) string {
	// Make a copy without the body to print just the signature.
	cp := *decl
	cp.Body = nil
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, &cp); err != nil {
		// Fallback: build manually.
		return "func " + decl.Name.Name + "(...)"
	}
	return strings.TrimSpace(buf.String())
}

// extractGenDecl extracts symbols from a general declaration (type, const, var).
func extractGenDecl(fset *token.FileSet, decl *ast.GenDecl, pkgName, filePath string, src []byte) []Symbol {
	var syms []Symbol

	switch decl.Tok {
	case token.TYPE:
		for _, spec := range decl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			sym := Symbol{
				Name:     ts.Name.Name,
				Package:  pkgName,
				FilePath: filePath,
			}

			switch ts.Type.(type) {
			case *ast.StructType:
				sym.Kind = "struct"
			case *ast.InterfaceType:
				sym.Kind = "interface"
			default:
				sym.Kind = "type"
			}

			// Doc comment: prefer spec-level, fall back to decl-level.
			if ts.Doc != nil {
				sym.DocComment = ts.Doc.Text()
			} else if decl.Doc != nil {
				sym.DocComment = decl.Doc.Text()
			}

			// Signature: the full type declaration.
			sym.Signature = typeSignature(fset, decl, ts, src)

			// Line range.
			startPos := fset.Position(ts.Pos())
			endPos := fset.Position(ts.End())
			// If the decl has a doc comment, include it in the line range.
			if decl.Doc != nil {
				docStart := fset.Position(decl.Doc.Pos())
				if docStart.Line < startPos.Line {
					startPos.Line = docStart.Line
				}
			}
			sym.LineStart = startPos.Line
			sym.LineEnd = endPos.Line

			// Body: source text.
			sym.Body = sourceSlice(src, fset, ts.Pos(), ts.End())

			syms = append(syms, sym)
		}

	case token.CONST, token.VAR:
		kind := "const"
		if decl.Tok == token.VAR {
			kind = "var"
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				sym := Symbol{
					Name:     name.Name,
					Kind:     kind,
					Package:  pkgName,
					FilePath: filePath,
				}

				// Doc comment: prefer spec-level, fall back to decl-level.
				if vs.Doc != nil {
					sym.DocComment = vs.Doc.Text()
				} else if decl.Doc != nil {
					sym.DocComment = decl.Doc.Text()
				}

				sym.Signature = valueSignature(kind, vs, i)

				startPos := fset.Position(vs.Pos())
				endPos := fset.Position(vs.End())
				if decl.Doc != nil {
					docStart := fset.Position(decl.Doc.Pos())
					if docStart.Line < startPos.Line {
						startPos.Line = docStart.Line
					}
				}
				sym.LineStart = startPos.Line
				sym.LineEnd = endPos.Line

				sym.Body = sourceSlice(src, fset, vs.Pos(), vs.End())

				syms = append(syms, sym)
			}
		}
	}

	return syms
}

// typeSignature builds a signature string for a type declaration.
func typeSignature(fset *token.FileSet, decl *ast.GenDecl, ts *ast.TypeSpec, src []byte) string {
	// For single-spec decls, print the whole GenDecl for a clean result.
	if len(decl.Specs) == 1 {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, decl); err == nil {
			return strings.TrimSpace(buf.String())
		}
	}
	// Fallback: reconstruct from source.
	return "type " + ts.Name.Name + " " + sourceSlice(src, fset, ts.Type.Pos(), ts.Type.End())
}

// valueSignature builds a signature string for a const or var.
func valueSignature(kind string, vs *ast.ValueSpec, nameIdx int) string {
	var buf strings.Builder
	buf.WriteString(kind)
	buf.WriteString(" ")
	buf.WriteString(vs.Names[nameIdx].Name)

	if vs.Type != nil {
		buf.WriteString(" ")
		buf.WriteString(exprString(vs.Type))
	}

	if nameIdx < len(vs.Values) {
		buf.WriteString(" = ")
		buf.WriteString(exprString(vs.Values[nameIdx]))
	}

	return buf.String()
}

// exprString returns a short string representation of an expression.
func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.BasicLit:
		return e.Value
	case *ast.IndexExpr:
		return exprString(e.X) + "[" + exprString(e.Index) + "]"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// sourceSlice returns the source text between two positions.
func sourceSlice(src []byte, fset *token.FileSet, start, end token.Pos) string {
	s := fset.Position(start).Offset
	e := fset.Position(end).Offset
	if s < 0 || e < 0 || s >= len(src) || e > len(src) || s > e {
		return ""
	}
	return string(src[s:e])
}
