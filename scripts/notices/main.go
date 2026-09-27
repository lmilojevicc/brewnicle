// Command notices collects licenses for the four supported release targets.
package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var noticeName = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents|attrib)([._-].*)?$`)
var licenseName = regexp.MustCompile(`(?i)^(licen[cs]e|copying)([._-].*)?$`)
var legalComment = regexp.MustCompile(`(?i)copyright|licen[cs]e|unicode\.org|public domain|permission|redistribution`)

type module struct {
	Path, Version, Dir string
	Main               bool
	Replace            *module
}

type pkg struct {
	Dir     string
	GoFiles []string
	Module  *module
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/notices OUTPUT_DIRECTORY")
		os.Exit(1)
	}
	if err := collect(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func collect(out string) error {
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		return fmt.Errorf("output must not exist: %s", out)
	}
	modules := map[string]*module{}
	sources := map[string]map[string]bool{}
	for _, target := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		parts := strings.Split(target, "/")
		cmd := exec.Command("go", "list", "-mod=readonly", "-deps", "-json", "./cmd/brewnicle")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1])
		data, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("list %s: %w", target, err)
		}
		dec := json.NewDecoder(strings.NewReader(string(data)))
		for {
			var p pkg
			if err := dec.Decode(&p); err == io.EOF {
				break
			} else if err != nil {
				return err
			}
			m := p.Module
			if m == nil || m.Main {
				continue
			}
			if m.Replace != nil || m.Dir == "" || m.Version == "" {
				return fmt.Errorf("unversioned/replaced module: %s", m.Path)
			}
			key := m.Path + "@" + m.Version
			modules[key] = m
			if sources[key] == nil {
				sources[key] = map[string]bool{}
			}
			for _, file := range p.GoFiles {
				sources[key][filepath.Join(p.Dir, file)] = true
			}
		}
	}
	keys := make([]string, 0, len(modules))
	for key := range modules {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		m := modules[key]
		count := 0
		err := filepath.WalkDir(m.Dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() || !noticeName.MatchString(entry.Name()) {
				return nil
			}
			rel, err := filepath.Rel(m.Dir, path)
			if err != nil {
				return err
			}
			if licenseName.MatchString(entry.Name()) {
				count++
			}
			return copyFile(path, filepath.Join(out, key, rel))
		})
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("no license found for %s", key)
		}
		files := make([]string, 0, len(sources[key]))
		for file := range sources[key] {
			files = append(files, file)
		}
		sort.Strings(files)
		var comments strings.Builder
		for _, file := range files {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			for _, group := range parsed.Comments {
				text := group.Text()
				if legalComment.MatchString(text) {
					rel, _ := filepath.Rel(m.Dir, file)
					fmt.Fprintf(&comments, "\n--- %s ---\n%s", rel, text)
				}
			}
		}
		if err := os.WriteFile(filepath.Join(out, key, "SOURCE_NOTICES.txt"), []byte(comments.String()), 0644); err != nil {
			return err
		}
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}
	goRoot := strings.TrimSpace(string(goroot))
	if err := copyFile(filepath.Join(goRoot, "LICENSE"), filepath.Join(out, "go", "LICENSE")); err != nil {
		return err
	}
	// The standard library also vendors components with their own notices.
	if err := filepath.WalkDir(filepath.Join(goRoot, "src"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || !noticeName.MatchString(entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(goRoot, path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(out, "go", rel))
	}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "MODULES.txt"), []byte(strings.Join(keys, "\n")+"\n"), 0644); err != nil {
		return err
	}
	return filepath.WalkDir("licenses", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel("licenses", path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(out, "supplemental", rel))
	})
}

func copyFile(source, dest string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0644)
}
