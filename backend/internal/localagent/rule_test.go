package localagent_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const generatePackage = "github.com/aoagents/agent-orchestrator/backend/internal/localagent/generate"

// backendRoot finds the backend module from this test's working directory,
// which go test sets to the package directory.
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// TestOnlyTheRunnerImportsTheGenerationClient is the build-time half of the
// no-prompt rule. The generation client is the one piece of the harness that
// can make a model produce output, and its only entry point needs a Turn made
// from a person's message. If the daemon, the CLI, discovery or anything else
// imported it, that code could send a model a prompt Blue Ash AI wrote: a
// test, a warm-up, a title or a summary request. So nothing else may.
func TestOnlyTheRunnerImportsTheGenerationClient(t *testing.T) {
	root := backendRoot(t)
	allowed := map[string]bool{
		// The runner itself.
		filepath.Join(root, "internal", "localagent"): true,
		// The client's own package and its tests.
		filepath.Join(root, "internal", "localagent", "generate"): true,
	}
	files := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "node_modules" || name == "testdata" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		files++
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			return nil
		}
		for _, spec := range parsed.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if imported == generatePackage && !allowed[filepath.Dir(path)] {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports the generation client. Only internal/localagent may: every model call must be part of a turn a person started.", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A walk that found nothing would pass for the wrong reason.
	if files < 100 {
		t.Fatalf("scanned only %d Go files under %s; the walk is not seeing the module", files, root)
	}
}
