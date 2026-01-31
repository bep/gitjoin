// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bep/helpers/envhelpers"
	"github.com/rogpeppe/go-internal/testscript"
)

func TestScripts(t *testing.T) {
	params := commonTestScriptsParam
	params.Dir = "testscripts"
	// params.TestWork = true
	// params.UpdateScripts = true
	testscript.Run(t, params)
}

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"gitjoin": main,
	})
}

func testSetupFunc() func(env *testscript.Env) error {
	sourceDir, _ := os.Getwd()
	isGitHubActions := os.Getenv("GITHUB_ACTIONS") != ""
	return func(env *testscript.Env) error {
		var keyVals []string
		// Add some environment variables to the test script.
		keyVals = append(keyVals, "SOURCE", sourceDir)
		keyVals = append(keyVals, "GITHUB_ACTIONS", fmt.Sprintf("%v", isGitHubActions))
		envhelpers.SetEnvVars(&env.Vars, keyVals...)

		return nil
	}
}

var commonTestScriptsParam = testscript.Params{
	Setup: func(env *testscript.Env) error {
		return testSetupFunc()(env)
	},
	Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
		// tree lists a directory recursively to stdout as a simple tree.
		"tree": func(ts *testscript.TestScript, neg bool, args []string) {
			dirname := ts.MkAbs(args[0])

			err := filepath.WalkDir(dirname, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() {
					return nil
				}
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				entries, err := os.ReadDir(path)
				if err != nil {
					return err
				}
				nodeType := "unknown"
				for _, entry := range entries {
					if !entry.IsDir() && entry.Name() == "gitjoin.txt" {
						nodeType = "gitjoin"
						break
					}
					if entry.IsDir() && entry.Name() == ".git" {
						nodeType = "git"
						break
					}
				}
				rel, err := filepath.Rel(dirname, path)
				if err != nil {
					return err
				}
				if rel == "." {
					fmt.Fprintf(ts.Stdout(), ". (%s)\n", nodeType)
					return nil
				}
				depth := strings.Count(rel, string(os.PathSeparator))
				prefix := strings.Repeat("  ", depth) + "└─"
				if d.IsDir() {
					fmt.Fprintf(ts.Stdout(), "%s%s:%s/\n", prefix, nodeType, d.Name())
				} else {
					fmt.Fprintf(ts.Stdout(), "%s%s:%s\n", prefix, nodeType, d.Name())
				}
				if nodeType == "git" {
					return filepath.SkipDir
				}
				return nil
			})
			if err != nil {
				ts.Fatalf("%v", err)
			}
		},
		// append appends to a file with a leading newline.
		"append": func(ts *testscript.TestScript, neg bool, args []string) {
			if len(args) < 2 {
				ts.Fatalf("usage: append FILE TEXT")
			}

			filename := ts.MkAbs(args[0])
			words := args[1:]
			for i, word := range words {
				words[i] = strings.Trim(word, "\"")
			}
			text := strings.Join(words, " ")

			_, err := os.Stat(filename)
			if err != nil {
				if os.IsNotExist(err) {
					ts.Fatalf("file does not exist: %s", filename)
				}
				ts.Fatalf("failed to stat file: %v", err)
			}

			f, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				ts.Fatalf("failed to open file: %v", err)
			}
			defer f.Close()

			_, err = f.WriteString("\n" + text)
			if err != nil {
				ts.Fatalf("failed to write to file: %v", err)
			}
		},
		// dostounix converts \r\n to \n.
		"dostounix": func(ts *testscript.TestScript, neg bool, args []string) {
			filename := ts.MkAbs(args[0])
			b, err := os.ReadFile(filename)
			if err != nil {
				ts.Fatalf("%v", err)
			}
			b = bytes.Replace(b, []byte("\r\n"), []byte{'\n'}, -1)
			if err := os.WriteFile(filename, b, 0o666); err != nil {
				ts.Fatalf("%v", err)
			}
		},
	},
}
