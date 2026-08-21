package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedJobcronUserBuildIgnoresAmbientSourcesAndGoSettings(t *testing.T) {
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
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=review-test", "-c", "user.email=review@example.invalid", "commit", "-qm", "reviewed"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	shaOutput, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	reviewedSHA := strings.TrimSpace(string(shaOutput))
	if err := os.WriteFile(filepath.Join(commandDir, "ignored.go"), []byte("package main\nfunc ignored(\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goEnv := filepath.Join(repo, "ambient-go-env")
	if err := os.WriteFile(goEnv, []byte("GOFLAGS=-tags=ambient\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "jobcron-user")
	cmd := exec.Command("sh", "build-reviewed-jobcron-user.sh", repo, reviewedSHA, output)
	cmd.Env = append(withoutEnv(os.Environ(), "GOENV", "GOFLAGS", "GOWORK"),
		"GOENV="+goEnv,
		"GOFLAGS=-tags=ambient",
		"GOWORK="+filepath.Join(repo, "missing.work"),
	)
	if buildOutput, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reviewed build: %v\n%s", err, buildOutput)
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
