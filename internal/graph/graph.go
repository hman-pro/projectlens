// Package graph builds call graphs from type-checked Go packages and extracts
// dependency edges using SSA and CHA analysis.
package graph

import (
	"context"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Edge represents a dependency relationship between two symbols.
type Edge struct {
	SourceName    string // e.g., "Bar"
	SourcePackage string // e.g., "example.com/test/pkg/foo"
	SourceFile    string // absolute path
	TargetName    string
	TargetPackage string
	TargetFile    string
	EdgeType      string // "calls", "implements", "imports"
}

// BuildResult holds the edges extracted from call graph analysis.
type BuildResult struct {
	Edges []Edge
}

// Build loads Go packages from dir matching the given patterns, builds an SSA
// program, runs CHA call graph analysis, and extracts dependency edges.
func Build(ctx context.Context, dir string, patterns []string) (*BuildResult, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("graph: directory %q: %w", dir, err)
	}

	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedModule,
		Context: ctx,
		Dir:     dir,
		Fset:    fset,
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("graph: loading packages: %w", err)
	}

	// Check for package-level errors.
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			return nil, fmt.Errorf("graph: package %s: %s", pkg.PkgPath, e)
		}
	}

	// Determine the module path for filtering.
	modulePath := ""
	for _, pkg := range pkgs {
		if pkg.Module != nil {
			modulePath = pkg.Module.Path
			break
		}
	}
	if modulePath == "" {
		// No module found — return empty result.
		return &BuildResult{}, nil
	}

	result := &BuildResult{}

	// Build SSA program.
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	// Run CHA call graph analysis.
	cg := cha.CallGraph(prog)

	// Extract "calls" edges from the call graph.
	extractCallEdges(cg, modulePath, fset, result)

	// Extract "implements" edges from type info.
	_ = ssaPkgs // SSA packages used above; types come from go/packages.
	extractImplementsEdges(pkgs, modulePath, fset, result)

	// Extract "imports" edges from package imports.
	extractImportEdges(pkgs, modulePath, result)

	return result, nil
}

// extractCallEdges walks the CHA call graph and extracts "calls" edges.
func extractCallEdges(cg *callgraph.Graph, modulePath string, fset *token.FileSet, result *BuildResult) {
	for fn, node := range cg.Nodes {
		if fn == nil {
			continue
		}

		callerPkg := fn.Package()
		if callerPkg == nil || !isInModule(callerPkg.Pkg.Path(), modulePath) {
			continue
		}

		// Skip synthetic and init functions.
		if fn.Synthetic != "" || isInitFunc(fn.Name()) {
			continue
		}

		for _, edge := range node.Out {
			calleeFn := edge.Callee.Func
			if calleeFn == nil {
				continue
			}

			calleePkg := calleeFn.Package()
			if calleePkg == nil || !isInModule(calleePkg.Pkg.Path(), modulePath) {
				continue
			}

			// Skip synthetic and init callees.
			if calleeFn.Synthetic != "" || isInitFunc(calleeFn.Name()) {
				continue
			}

			callerName := cleanFuncName(fn.Name())
			calleeName := cleanFuncName(calleeFn.Name())

			e := Edge{
				SourceName:    callerName,
				SourcePackage: callerPkg.Pkg.Path(),
				SourceFile:    funcFile(fn, fset),
				TargetName:    calleeName,
				TargetPackage: calleePkg.Pkg.Path(),
				TargetFile:    funcFile(calleeFn, fset),
				EdgeType:      "calls",
			}
			result.Edges = append(result.Edges, e)
		}
	}
}

// extractImplementsEdges finds all named types that implement interfaces
// within the loaded packages.
func extractImplementsEdges(pkgs []*packages.Package, modulePath string, fset *token.FileSet, result *BuildResult) {
	// Collect all named types and interfaces from the loaded packages.
	type namedInfo struct {
		named   *types.Named
		pkgPath string
		file    string
	}

	var namedTypes []namedInfo
	var ifaces []namedInfo

	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		if !isInModule(pkg.PkgPath, modulePath) {
			continue
		}

		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			tn, ok := obj.(*types.TypeName)
			if !ok {
				continue
			}
			named, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}

			pos := fset.Position(obj.Pos())
			info := namedInfo{
				named:   named,
				pkgPath: pkg.PkgPath,
				file:    pos.Filename,
			}

			if types.IsInterface(named.Underlying()) {
				// Only consider non-empty interfaces.
				iface := named.Underlying().(*types.Interface)
				if iface.NumMethods() > 0 {
					ifaces = append(ifaces, info)
				}
			} else {
				namedTypes = append(namedTypes, info)
			}
		}
	}

	// Check each named type against each interface.
	for _, nt := range namedTypes {
		for _, iface := range ifaces {
			ifaceType := iface.named.Underlying().(*types.Interface)

			// Check T or *T implements the interface.
			if types.Implements(nt.named, ifaceType) || types.Implements(types.NewPointer(nt.named), ifaceType) {
				e := Edge{
					SourceName:    nt.named.Obj().Name(),
					SourcePackage: nt.pkgPath,
					SourceFile:    nt.file,
					TargetName:    iface.named.Obj().Name(),
					TargetPackage: iface.pkgPath,
					TargetFile:    iface.file,
					EdgeType:      "implements",
				}
				result.Edges = append(result.Edges, e)
			}
		}
	}
}

// extractImportEdges creates edges for each in-module import relationship.
func extractImportEdges(pkgs []*packages.Package, modulePath string, result *BuildResult) {
	// Build a set of packages that were directly loaded (not just deps).
	visited := make(map[string]bool)

	var walk func(pkg *packages.Package)
	walk = func(pkg *packages.Package) {
		if visited[pkg.PkgPath] {
			return
		}
		visited[pkg.PkgPath] = true

		if !isInModule(pkg.PkgPath, modulePath) {
			return
		}

		sourceFile := ""
		if len(pkg.GoFiles) > 0 {
			sourceFile = pkg.GoFiles[0]
		}

		for importPath, importedPkg := range pkg.Imports {
			if !isInModule(importPath, modulePath) {
				continue
			}

			targetFile := ""
			if len(importedPkg.GoFiles) > 0 {
				targetFile = importedPkg.GoFiles[0]
			}

			e := Edge{
				SourceName:    pkg.Name,
				SourcePackage: pkg.PkgPath,
				SourceFile:    sourceFile,
				TargetName:    importedPkg.Name,
				TargetPackage: importPath,
				TargetFile:    targetFile,
				EdgeType:      "imports",
			}
			result.Edges = append(result.Edges, e)

			// Walk imported packages too.
			walk(importedPkg)
		}
	}

	for _, pkg := range pkgs {
		walk(pkg)
	}
}

// isInModule checks if a package path belongs to the given module.
func isInModule(pkgPath, modulePath string) bool {
	return pkgPath == modulePath ||
		(len(pkgPath) > len(modulePath) && pkgPath[:len(modulePath)] == modulePath && pkgPath[len(modulePath)] == '/')
}

// isInitFunc returns true for init and init$N function names.
func isInitFunc(name string) bool {
	return name == "init" || strings.HasPrefix(name, "init$") || strings.HasPrefix(name, "init#")
}

// cleanFuncName strips receiver type prefixes from SSA function names.
// SSA names look like "(pkg.Type).Method" — we extract just "Method".
func cleanFuncName(name string) string {
	// SSA function names for methods include the receiver, e.g. "(*SimpleProcessor).Run".
	// We want just the method/function name.
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// funcFile returns the file path of an SSA function.
func funcFile(fn *ssa.Function, fset *token.FileSet) string {
	if fn.Pos().IsValid() {
		return fset.Position(fn.Pos()).Filename
	}
	if fn.Package() != nil {
		// Try to get the first file of the package.
		if pkg := fn.Package().Pkg; pkg != nil {
			scope := pkg.Scope()
			for _, name := range scope.Names() {
				obj := scope.Lookup(name)
				if obj.Pos().IsValid() {
					pos := fset.Position(obj.Pos())
					return pos.Filename
				}
			}
		}
	}
	return ""
}
