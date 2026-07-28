package production

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	testImage            = "ghcr.io/example-owner/jobcron@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testDatabaseURL      = "postgres://user:pass@db.example.invalid:5432/jobcron?sslmode=require"
	testSessionSecret    = "synthetic-session-secret-at-least-32-bytes"
	testCredentialKey    = "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="
	testDailyTime        = "06:15"
	testSignupAccessCode = "synthetic-cohort-code"
	testSponsorUserID    = "42"
	testProxySecret      = "synthetic-proxy-secret"
	credentialKeyEnvName = "JOBCRON_CREDENTIAL_ENCRYPTION_KEY"
	proxySecretEnvName   = "JOBCRON_PROXY_SECRET"
)

type composeConfig struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]any            `yaml:"volumes"`
	Networks map[string]composeNetwork `yaml:"networks"`
}

type composeService struct {
	Image       string            `yaml:"image"`
	Build       any               `yaml:"build"`
	Command     []string          `yaml:"command"`
	Environment map[string]string `yaml:"environment"`
	Volumes     []composeVolume   `yaml:"volumes"`
	Ports       []composePort     `yaml:"ports"`
	Networks    map[string]any    `yaml:"networks"`
	Logging     composeLogging    `yaml:"logging"`
}

type composeVolume struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
}

type composePort struct {
	HostIP    string `yaml:"host_ip"`
	Published string `yaml:"published"`
	Target    int    `yaml:"target"`
}

type composeLogging struct {
	Driver  string            `yaml:"driver"`
	Options map[string]string `yaml:"options"`
}

type composeNetwork struct {
	Internal bool `yaml:"internal"`
}

func TestProductionComposeRequiresCredentialEncryptionKey(t *testing.T) {
	cmd := composeCommand(false)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("docker compose config succeeded without JOBCRON_CREDENTIAL_ENCRYPTION_KEY")
	}
	if !strings.Contains(string(output), credentialKeyEnvName) {
		t.Fatalf("docker compose config failed without naming %s:\n%s", credentialKeyEnvName, output)
	}

	config := renderCompose(t)
	if got := config.Services["app"].Environment[credentialKeyEnvName]; got != testCredentialKey {
		t.Fatalf("%s = %q, want rendered synthetic key", credentialKeyEnvName, got)
	}
}

func TestProductionComposeHasNoJobcronConfigVolumeOrMount(t *testing.T) {
	config := renderCompose(t)
	app := config.Services["app"]
	for _, volume := range app.Volumes {
		if volume.Source == "jobcron_config" || volume.Target == "/root/.config/jobcron" {
			t.Fatalf("app retains filesystem credential storage mount: source=%q target=%q", volume.Source, volume.Target)
		}
	}
	if _, ok := config.Volumes["jobcron_config"]; ok {
		t.Fatal("top-level jobcron_config volume is still declared")
	}
}

func TestProductionComposeRetainsDatabaseSessionAndCaddyState(t *testing.T) {
	config := renderCompose(t)
	app := config.Services["app"]
	if got := app.Environment["DATABASE_URL"]; got != testDatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, testDatabaseURL)
	}
	if got := app.Environment["SESSION_SECRET"]; got != testSessionSecret {
		t.Fatalf("SESSION_SECRET = %q, want %q", got, testSessionSecret)
	}

	caddy := config.Services["caddy"]
	wantVolumes := map[string]string{"/data": "caddy_data", "/config": "caddy_config"}
	for _, volume := range caddy.Volumes {
		if wantSource, ok := wantVolumes[volume.Target]; ok {
			if volume.Source != wantSource {
				t.Errorf("caddy volume target %s uses source %q, want %q", volume.Target, volume.Source, wantSource)
			}
			delete(wantVolumes, volume.Target)
		}
	}
	for target := range wantVolumes {
		t.Errorf("caddy volume target %s is missing", target)
	}
	for _, name := range []string{"caddy_data", "caddy_config"} {
		if _, ok := config.Volumes[name]; !ok {
			t.Errorf("top-level volume %s is missing", name)
		}
	}
}

func TestProductionComposeUsesImmutableImageReference(t *testing.T) {
	config := renderCompose(t)
	immutableImage := regexp.MustCompile(`^ghcr\.io/[a-z0-9._-]+/jobcron@sha256:[a-f0-9]{64}$`)
	if got := config.Services["app"].Image; !immutableImage.MatchString(got) {
		t.Fatalf("app image = %q, want private GHCR sha256 digest", got)
	}
	if got := config.Services["app"].Image; got != testImage {
		t.Fatalf("app image = %q, want requested candidate %q", got, testImage)
	}
	if config.Services["app"].Build != nil {
		t.Fatal("app retains a local build configuration")
	}
}

func TestProductionComposeBindsOnlyLoopbackPorts(t *testing.T) {
	config := renderCompose(t)
	want := map[string]composePort{
		"app":   {HostIP: "127.0.0.1", Published: "7777", Target: 7777},
		"caddy": {HostIP: "127.0.0.1", Published: "8443", Target: 443},
	}
	for service, expected := range want {
		ports := config.Services[service].Ports
		if len(ports) != 1 || ports[0] != expected {
			t.Errorf("%s ports = %#v, want only %#v", service, ports, expected)
		}
	}
}

func TestProductionComposeUsesInternalRuntimeNetworkAndDisablesMetadata(t *testing.T) {
	config := renderCompose(t)
	network, ok := config.Networks["runtime"]
	if !ok || !network.Internal {
		t.Fatalf("runtime network = %#v (present %v), want internal network", network, ok)
	}
	for _, service := range []string{"app", "caddy"} {
		if _, ok := config.Services[service].Networks["runtime"]; !ok {
			t.Errorf("%s is not attached to the internal runtime network", service)
		}
		if got := config.Services[service].Environment["AWS_EC2_METADATA_DISABLED"]; got != "true" {
			t.Errorf("%s AWS_EC2_METADATA_DISABLED = %q, want true", service, got)
		}
	}
}

func TestProductionComposeRotatesLocalLogs(t *testing.T) {
	config := renderCompose(t)
	for _, service := range []string{"app", "caddy"} {
		logging := config.Services[service].Logging
		if logging.Driver != "json-file" ||
			logging.Options["max-size"] != "10m" ||
			logging.Options["max-file"] != "3" {
			t.Errorf("%s logging = %#v, want json-file rotation 10m x 3", service, logging)
		}
	}
}

func TestProductionComposeMountsOriginCertificateReadOnly(t *testing.T) {
	caddy := renderCompose(t).Services["caddy"]
	found := false
	for _, volume := range caddy.Volumes {
		if volume.Source == "/run/jobcron/caddy" {
			found = true
			if volume.Type != "bind" || volume.Target != "/run/jobcron/caddy" || !volume.ReadOnly {
				t.Errorf("origin certificate mount = %#v, want read-only bind", volume)
			}
		}
	}
	if !found {
		t.Fatal("Caddy has no /run/jobcron/caddy origin certificate mount")
	}

	caddyfile, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatalf("read Caddyfile: %v", err)
	}
	want := "tls /run/jobcron/caddy/origin.crt /run/jobcron/caddy/origin.key"
	if got := strings.Count(string(caddyfile), want); got != 2 {
		t.Fatalf("Caddyfile Origin CA TLS directives = %d, want 2", got)
	}
}

func TestProductionComposeExampleDocumentsTransientEnvironment(t *testing.T) {
	example, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	text := string(example)
	if !strings.Contains(text, "/run/jobcron/compose.env") {
		t.Fatal(".env.example does not identify the transient production environment path")
	}
	if regexp.MustCompile(`(?m)^\s*(cp|install)\b`).MatchString(text) {
		t.Fatal(".env.example contains a command that persists the synthetic environment")
	}
}

func TestProductionComposePreservesDailyTimeAndCommand(t *testing.T) {
	app := renderCompose(t).Services["app"]
	if got := app.Environment["JOBCRON_DAILY_SCRAPE_TIME"]; got != testDailyTime {
		t.Fatalf("JOBCRON_DAILY_SCRAPE_TIME = %q, want preserved %q", got, testDailyTime)
	}
	wantCommand := []string{"--no-open", "--host", "0.0.0.0", "--port", "7777"}
	if strings.Join(app.Command, "\x00") != strings.Join(wantCommand, "\x00") {
		t.Fatalf("app command = %q, want %q", app.Command, wantCommand)
	}
}

func TestProductionComposePassesCohortRuntimeVariables(t *testing.T) {
	app := renderCompose(t).Services["app"]
	if got := app.Environment["JOBCRON_SIGNUP_ACCESS_CODE"]; got != testSignupAccessCode {
		t.Fatalf("JOBCRON_SIGNUP_ACCESS_CODE = %q, want preserved synthetic code", got)
	}
	if got := app.Environment["JOBCRON_STAGE1_SPONSOR_USER_ID"]; got != testSponsorUserID {
		t.Fatalf("JOBCRON_STAGE1_SPONSOR_USER_ID = %q, want %q", got, testSponsorUserID)
	}
}

func TestProductionComposeSharesRequiredProxySecret(t *testing.T) {
	config := renderCompose(t)
	for _, service := range []string{"app", "caddy"} {
		if got := config.Services[service].Environment[proxySecretEnvName]; got != testProxySecret {
			t.Fatalf("%s %s = %q, want shared synthetic secret", service, proxySecretEnvName, got)
		}
	}
	caddyfile, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatalf("read Caddyfile: %v", err)
	}
	want := "header_up X-Jobcron-Proxy {$JOBCRON_PROXY_SECRET}"
	if !strings.Contains(string(caddyfile), want) {
		t.Fatalf("Caddyfile does not overwrite the trusted proxy header with %q", want)
	}
}

func TestProductionComposeLeavesCohortRuntimeVariablesUnset(t *testing.T) {
	output, err := composeCommand(true).CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}
	var config struct {
		Services map[string]struct {
			Environment map[string]*string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(output, &config); err != nil {
		t.Fatalf("parse rendered compose config: %v\n%s", err, output)
	}
	app := config.Services["app"]
	for _, name := range []string{"JOBCRON_SIGNUP_ACCESS_CODE", "JOBCRON_STAGE1_SPONSOR_USER_ID"} {
		value, ok := app.Environment[name]
		if !ok || value != nil {
			t.Errorf("%s = %v (present %v), want explicit null passthrough", name, value, ok)
		}
	}
}

func renderCompose(t *testing.T) composeConfig {
	t.Helper()
	cmd := composeCommand(true)
	cmd.Env = append(cmd.Env,
		"JOBCRON_SIGNUP_ACCESS_CODE="+testSignupAccessCode,
		"JOBCRON_STAGE1_SPONSOR_USER_ID="+testSponsorUserID,
		proxySecretEnvName+"="+testProxySecret,
	)
	return renderComposeCommand(t, cmd)
}

func renderComposeCommand(t *testing.T, cmd *exec.Cmd) composeConfig {
	t.Helper()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}

	var config composeConfig
	if err := yaml.Unmarshal(output, &config); err != nil {
		t.Fatalf("parse rendered compose config: %v\n%s", err, output)
	}
	return config
}

func composeCommand(includeCredentialKey bool) *exec.Cmd {
	cmd := exec.Command("docker", "compose", "-f", "compose.yaml", "config")
	cmd.Env = withoutEnvironment(os.Environ(),
		"JOBCRON_IMAGE",
		"DATABASE_URL",
		"SESSION_SECRET",
		credentialKeyEnvName,
		"JOBCRON_DAILY_SCRAPE_TIME",
		"JOBCRON_SIGNUP_ACCESS_CODE",
		"JOBCRON_STAGE1_SPONSOR_USER_ID",
		proxySecretEnvName,
	)
	cmd.Env = append(cmd.Env,
		"JOBCRON_IMAGE="+testImage,
		"DATABASE_URL="+testDatabaseURL,
		"SESSION_SECRET="+testSessionSecret,
		"JOBCRON_DAILY_SCRAPE_TIME="+testDailyTime,
		proxySecretEnvName+"="+testProxySecret,
	)
	if includeCredentialKey {
		cmd.Env = append(cmd.Env, credentialKeyEnvName+"="+testCredentialKey)
	}
	return cmd
}

func withoutEnvironment(environment []string, names ...string) []string {
	excluded := make(map[string]struct{}, len(names))
	for _, name := range names {
		excluded[name] = struct{}{}
	}

	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := excluded[name]; !ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
