package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// affected is the set of test packages a change can reach: the packages
// whose files changed and every package with tests that imports one of
// them, directly or through its dependencies. A change to an input every
// package shares (go.mod, the schema, the migrations, the test
// environment) reaches all of them.
type affected struct {
	// All is every package: the change touches a shared input, or there
	// is no git history to compare against. Why says which.
	All bool
	Why string
	// Packages are the import paths to test when not All; Total counts
	// the packages with tests.
	Packages []string
	Total    int
	// Changed lists the files the selection was made from.
	Changed []string
}

// sharedInputs are files whose change reaches every test.
var sharedInputs = []string{"go.mod", "go.sum", "go.work", "go.work.sum", "schema.lidza", "lidza.json", ".env.test", ".env"}

// sharedDirs are directories whose files reach every test: the database
// schema and queries (sqlc), and the packs' modules.
var sharedDirs = []string{"db/", "packs/", "schema/"}

// changedFiles lists the files a selection compares: the index against
// HEAD (staged, what a commit would hold), or the working tree and the
// untracked files against base (a ref; "" is HEAD).
func changedFiles(ctx context.Context, dir, base string, staged bool) ([]string, error) {
	if _, err := git(dir, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return nil, fmt.Errorf("no commit yet")
	}
	var args []string
	if staged {
		args = []string{"diff", "--cached", "--name-only", "--no-renames", "--relative", "HEAD", "--"}
	} else {
		if base == "" {
			base = "HEAD"
		}
		args = []string{"diff", "--name-only", "--no-renames", "--relative", base, "--"}
	}
	out, err := git(dir, args...)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args[:2], " "), err)
	}
	files := strings.Fields(out)
	if !staged {
		if untracked, err := git(dir, "ls-files", "--others", "--exclude-standard"); err == nil {
			files = append(files, strings.Fields(untracked)...)
		}
	}
	sort.Strings(files)
	return files, nil
}

// goPackage is what `go list -json` says about one package of the app.
type goPackage struct {
	ImportPath   string
	Dir          string
	Deps         []string
	TestImports  []string
	XTestImports []string
	TestGoFiles  []string
	XTestGoFiles []string
}

func listPackages(ctx context.Context, dir string) ([]goPackage, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json=ImportPath,Dir,Deps,TestImports,XTestImports,TestGoFiles,XTestGoFiles", "./...")
	cmd.Dir = dir
	cmd.Env = testEnv(dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var pkgs []goPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			break
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// affectedPackages selects the test packages for the change described
// by base and staged (changedFiles).
func affectedPackages(ctx context.Context, dir, base string, staged bool) (affected, error) {
	files, err := changedFiles(ctx, dir, base, staged)
	if err != nil {
		return affected{All: true, Why: err.Error()}, nil
	}
	a := affected{Changed: files}
	for _, f := range files {
		for _, s := range sharedInputs {
			if f == s {
				return affected{All: true, Why: f + " changed", Changed: files}, nil
			}
		}
		for _, d := range sharedDirs {
			if strings.HasPrefix(f, d) {
				return affected{All: true, Why: d + " changed", Changed: files}, nil
			}
		}
	}
	pkgs, err := listPackages(ctx, dir)
	if err != nil {
		return affected{All: true, Why: err.Error(), Changed: files}, nil
	}
	abs, _ := filepath.Abs(dir)
	byDir := map[string]*goPackage{}
	byPath := map[string]*goPackage{}
	for i := range pkgs {
		p := &pkgs[i]
		if rel, err := filepath.Rel(abs, p.Dir); err == nil {
			byDir[filepath.ToSlash(rel)] = p
		}
		byPath[p.ImportPath] = p
		if len(p.TestGoFiles)+len(p.XTestGoFiles) > 0 {
			a.Total++
		}
	}
	// The packages whose directories hold a changed file: any file, since
	// a package embeds its templates and fixtures.
	changed := map[string]bool{}
	for _, f := range files {
		d := path.Dir(f)
		if d == "." {
			d = ""
		}
		if p, ok := byDir[d]; ok {
			changed[p.ImportPath] = true
		} else if p, ok := byDir["."]; ok && d == "" {
			changed[p.ImportPath] = true
		}
	}
	if len(changed) == 0 {
		return a, nil
	}
	reaches := func(p *goPackage) bool {
		if changed[p.ImportPath] {
			return true
		}
		for _, d := range p.Deps {
			if changed[d] {
				return true
			}
		}
		for _, imports := range [][]string{p.TestImports, p.XTestImports} {
			for _, ip := range imports {
				if changed[ip] {
					return true
				}
				if dep, ok := byPath[ip]; ok {
					for _, d := range dep.Deps {
						if changed[d] {
							return true
						}
					}
				}
			}
		}
		return false
	}
	for i := range pkgs {
		p := &pkgs[i]
		if len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		if reaches(p) {
			a.Packages = append(a.Packages, p.ImportPath)
		}
	}
	sort.Strings(a.Packages)
	return a, nil
}

// String says what was selected, for the report.
func (a affected) String() string {
	switch {
	case a.All:
		return "every package (" + a.Why + ")"
	case len(a.Packages) == 0:
		return fmt.Sprintf("no Go package reached by the change (%d file(s))", len(a.Changed))
	default:
		return fmt.Sprintf("%d of %d package(s) reached by the change", len(a.Packages), a.Total)
	}
}
