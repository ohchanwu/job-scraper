package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplacementHostRejectsUnverifiedSameVersionCompose(t *testing.T) {
	template, err := os.ReadFile(filepath.Join("..", "infra", "terraform", "production", "templates", "replacement-host.sh.tftpl"))
	if err != nil {
		t.Fatalf("read replacement-host template: %v", err)
	}

	start := strings.Index(string(template), "compose_version=")
	endMarker := "test \"$(docker compose version --short)\" = \"$compose_expected_version\""
	end := strings.Index(string(template), endMarker)
	if start < 0 || end < start {
		t.Fatal("compose bootstrap block not found")
	}
	composeBlock := string(template)[start : end+len(endMarker)]

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatalf("create mock bin: %v", err)
	}
	dockerLog := filepath.Join(root, "docker.log")
	writeBootstrapMock(t, binDir, "docker", `printf 'executed\n' >>"$DOCKER_LOG"
printf '5.5.0\n'`)
	writeBootstrapMock(t, binDir, "curl", `while [ "$#" -gt 0 ]; do
	if [ "$1" = "--output" ]; then shift; output=$1; fi
	shift
done
printf 'corrupt download' >"$output"`)
	writeBootstrapMock(t, binDir, "sha256sum", "exit 1")
	writeBootstrapMock(t, binDir, "install", "exit 0")

	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader("set -euo pipefail\n" + composeBlock + "\n")
	cmd.Env = append(os.Environ(), "PATH="+binDir+":/usr/bin:/bin", "DOCKER_LOG="+dockerLog)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("corrupt Compose download passed verification:\n%s", output)
	}
	if contents, err := os.ReadFile(dockerLog); err == nil && len(contents) > 0 {
		t.Fatalf("unverified pre-existing Compose plugin executed:\n%s", contents)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read Docker execution log: %v", err)
	}
}

func writeBootstrapMock(t *testing.T, binDir, name, body string) {
	t.Helper()
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write %s mock: %v", name, err)
	}
}
