package scripts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestReviewedJobcronUserBuildIgnoresAmbientSourcesAndGoSettings(t *testing.T) {
	repo, reviewedSHA := newReviewedBuildRepo(t)
	commandDir := filepath.Join(repo, "cmd", "jobcron-user")
	if err := os.WriteFile(filepath.Join(commandDir, "ignored.go"), []byte("package main\nfunc ignored(\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "main.go"), []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"dirty\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlayMain := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(overlayMain, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"overlay\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(t.TempDir(), "overlay.json")
	overlayJSON, err := json.Marshal(map[string]map[string]string{
		"Replace": {filepath.Join(commandDir, "main.go"): overlayMain},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayFile, overlayJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	goEnv := filepath.Join(repo, "ambient-go-env")
	if err := os.WriteFile(goEnv, []byte("GOFLAGS=-overlay="+overlayFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostileRoot := t.TempDir()
	hostileMarker := filepath.Join(hostileRoot, "ran")
	hostileTool := filepath.Join(hostileRoot, "hostile-tool")
	if err := os.WriteFile(hostileTool, []byte("#!/bin/sh\nprintf ran >\"$HOSTILE_MARKER\"\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostileRoot, "git"), []byte("#!/bin/sh\nprintf ran >\"$HOSTILE_MARKER\"\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := buildAndRunReviewed(t, repo, reviewedSHA,
		"PATH="+hostileRoot,
		"GOENV="+goEnv,
		"GOFLAGS=-overlay="+overlayFile,
		"GOWORK="+filepath.Join(repo, "missing.work"),
		"GOCACHEPROG="+hostileTool,
		"GOTOOLCHAIN=hostile",
		"GOEXPERIMENT=hostile",
		"GODEBUG=gocacheverify=1",
		"GOCACHE="+filepath.Join(hostileRoot, "cache"),
		"GOMODCACHE="+filepath.Join(hostileRoot, "modcache"),
		"GOPATH="+filepath.Join(hostileRoot, "gopath"),
		"CGO_ENABLED=1",
		"CC="+hostileTool,
		"HOSTILE_MARKER="+hostileMarker,
	)
	if _, err := os.Stat(hostileMarker); !os.IsNotExist(err) {
		t.Fatalf("ambient build helper executed: %v", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Fatalf("reviewed binary mode = %o, want 500", info.Mode().Perm())
	}
	binaryOutput, err := exec.Command(output).Output()
	if err != nil {
		t.Fatalf("run reviewed binary: %v", err)
	}
	if string(binaryOutput) != "reviewed\n" {
		t.Fatalf("reviewed binary output = %q", binaryOutput)
	}
}

func TestReviewedJobcronUserBuildIgnoresReplacementRefs(t *testing.T) {
	repo, reviewedSHA := newReviewedBuildRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "cmd", "jobcron-user", "main.go"), []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"replaced\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "commit", "-qam", "replacement")
	replacementSHA := gitOutput(t, repo, "rev-parse", "HEAD")
	gitRun(t, repo, "reset", "--hard", reviewedSHA)
	gitRun(t, repo, "replace", reviewedSHA, replacementSHA)

	output := buildAndRunReviewed(t, repo, reviewedSHA)
	assertReviewedBinary(t, output)
}

func TestReviewedJobcronUserBuildIgnoresRepositoryLocalAttributes(t *testing.T) {
	repo, reviewedSHA := newReviewedBuildRepo(t)
	attributes := filepath.Join(repo, ".git", "info", "attributes")
	if err := os.WriteFile(attributes, []byte("cmd/jobcron-user/main.go export-ignore\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output := buildAndRunReviewed(t, repo, reviewedSHA)
	assertReviewedBinary(t, output)
}

func TestReviewedJobcronUserBuildRejectsInvalidInputs(t *testing.T) {
	repo, reviewedSHA := newReviewedBuildRepo(t)
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err = filepath.Abs(goBinary)
	if err != nil {
		t.Fatal(err)
	}
	toolchainDigest := reviewedToolchainDigest(t, goBinary)
	output := filepath.Join(t.TempDir(), "jobcron-user")
	valid := []string{repo, reviewedSHA, output, goBinary, toolchainDigest}
	for name, args := range map[string][]string{
		"missing argument":         {repo, reviewedSHA, output},
		"invalid sha":              append([]string{repo, "not-a-sha"}, valid[2:]...),
		"relative output":          append([]string{repo, reviewedSHA, "jobcron-user"}, valid[3:]...),
		"relative go":              append([]string{repo, reviewedSHA, output, "go"}, valid[4:]...),
		"missing go":               append([]string{repo, reviewedSHA, output, filepath.Join(t.TempDir(), "go")}, valid[4:]...),
		"invalid toolchain digest": {repo, reviewedSHA, output, goBinary, "not-a-digest"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("sh", append([]string{"build-reviewed-jobcron-user.sh"}, args...)...)
			if buildOutput, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("reviewed build accepted invalid input:\n%s", buildOutput)
			}
		})
	}
}

func newReviewedBuildRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	commandDir := filepath.Join(repo, "cmd", "jobcron-user")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".gitignore":                          "/cmd/jobcron-user/ignored.go\n",
		"go.mod":                              "module example.com/review\n\ngo 1.23\n",
		"cmd/jobcron-user/main.go":            "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"reviewed\") }\n",
		"cmd/jobcron-user/ambient_failure.go": "//go:build ambient\n\npackage main\nfunc ambientFailure(\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "-c", "user.name=review-test", "-c", "user.email=review@example.invalid", "commit", "-qm", "reviewed")
	return repo, gitOutput(t, repo, "rev-parse", "HEAD")
}

func buildAndRunReviewed(t *testing.T, repo, reviewedSHA string, extraEnv ...string) string {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goBinary, err = filepath.Abs(goBinary)
	if err != nil {
		t.Fatal(err)
	}
	toolchainDigest := reviewedToolchainDigest(t, goBinary)
	output := filepath.Join(t.TempDir(), "jobcron-user")
	cmd := exec.Command("sh", "build-reviewed-jobcron-user.sh", repo, reviewedSHA, output, goBinary, toolchainDigest)
	cmd.Env = append(withoutEnv(os.Environ(),
		"PATH",
		"GOENV", "GOFLAGS", "GOWORK", "GOCACHEPROG", "GOTOOLCHAIN", "GOEXPERIMENT", "GODEBUG",
		"GOCACHE", "GOMODCACHE", "GOPATH", "CGO_ENABLED", "CC", "HOSTILE_MARKER",
	), extraEnv...)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reviewed build: %v\n%s", err, buildOutput)
	}
	return output
}

func reviewedToolchainDigest(t *testing.T, goBinary string) string {
	t.Helper()
	goRoot := filepath.Dir(filepath.Dir(goBinary))
	var paths []string
	for _, root := range []string{filepath.Join(goRoot, "bin"), filepath.Join(goRoot, "pkg", "tool")} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode().Perm()&0o111 != 0 {
					paths = append(paths, path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk Go toolchain: %v", err)
		}
	}
	sort.Strings(paths)
	var manifest []byte
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read Go toolchain file: %v", err)
		}
		digest := sha256.Sum256(contents)
		manifest = append(manifest, []byte(fmt.Sprintf("%x  %s\n", digest, strings.TrimPrefix(path, goRoot+string(filepath.Separator))))...)
	}
	digest := sha256.Sum256(manifest)
	return fmt.Sprintf("%x", digest)
}

func assertReviewedBinary(t *testing.T, output string) {
	t.Helper()
	binaryOutput, err := exec.Command(output).Output()
	if err != nil {
		t.Fatalf("run reviewed binary: %v", err)
	}
	if string(binaryOutput) != "reviewed\n" {
		t.Fatalf("reviewed binary output = %q", binaryOutput)
	}
}

func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
}
