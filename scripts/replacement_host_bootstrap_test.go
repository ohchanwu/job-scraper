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
	endMarker := "test \"$(docker --config \"$compose_config\" compose version --short)\" = \"$compose_expected_version\""
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

func TestDockerConfigExcludesHomeComposePlugin(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("Docker CLI unavailable: %v", err)
	}

	home := t.TempDir()
	configDir := t.TempDir()
	pluginDir := filepath.Join(home, ".docker", "cli-plugins")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("create user plugin directory: %v", err)
	}
	pluginLog := filepath.Join(home, "user-plugin.log")
	writeBootstrapMock(t, pluginDir, "docker-compose", `if [ "${1:-}" = docker-cli-plugin-metadata ]; then
	printf '%s\n' '{"SchemaVersion":"0.1.0","Vendor":"test","Version":"5.5.0","ShortDescription":"test"}'
	exit 0
fi
printf 'executed\n' >>"$USER_PLUGIN_LOG"
printf '5.5.0\n'`)

	env := append(withoutEnv(os.Environ(), "HOME", "DOCKER_CONFIG", "DOCKER_CLI_PLUGIN_EXTRA_DIRS"),
		"HOME="+home, "USER_PLUGIN_LOG="+pluginLog)
	control := exec.Command(docker, "compose", "version", "--short")
	control.Env = env
	if output, err := control.CombinedOutput(); err != nil {
		t.Fatalf("control user plugin was not discoverable: %v\n%s", err, output)
	}
	if contents, err := os.ReadFile(pluginLog); err != nil || len(contents) == 0 {
		t.Fatalf("control user plugin did not execute: %v", err)
	}
	if err := os.Remove(pluginLog); err != nil {
		t.Fatalf("clear control log: %v", err)
	}

	isolated := exec.Command(docker, "--config", configDir, "compose", "version", "--short")
	isolated.Env = env
	_ = isolated.Run()
	if contents, err := os.ReadFile(pluginLog); err == nil && len(contents) > 0 {
		t.Fatalf("isolated Docker config executed user plugin:\n%s", contents)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read isolated plugin log: %v", err)
	}
}

func writeBootstrapMock(t *testing.T, binDir, name, body string) {
	t.Helper()
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write %s mock: %v", name, err)
	}
}
