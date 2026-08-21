package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	productionVerifier      = "scripts/verify-production-compose.sh"
	productionTextInspector = "scripts/inspect-production-compose-render.sh"
	syntheticImage          = "ghcr.io/example-owner/jobcron@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	syntheticCommitTag      = "ghcr.io/example-owner/jobcron:sha-0123456789ab"
	syntheticDatabase       = "postgres://synthetic:synthetic@db.example.invalid/jobcron?sslmode=require"
	syntheticSession        = "synthetic-session-secret-at-least-32-bytes"
	syntheticKey            = "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="
	syntheticProxySecret    = "synthetic-proxy-secret"
	syntheticDailyTime      = "06:15"
	syntheticSignupCode     = "synthetic-cohort-code"
	syntheticSponsorUserID  = "42"
)

var syntheticProductionEnvironment = []string{
	"JOBCRON_IMAGE=" + syntheticImage,
	"DATABASE_URL=" + syntheticDatabase,
	"SESSION_SECRET=" + syntheticSession,
	"JOBCRON_CREDENTIAL_ENCRYPTION_KEY=" + syntheticKey,
	"JOBCRON_PROXY_SECRET=" + syntheticProxySecret,
	"JOBCRON_DAILY_SCRAPE_TIME=" + syntheticDailyTime,
	"JOBCRON_SIGNUP_ACCESS_CODE=" + syntheticSignupCode,
	"JOBCRON_STAGE1_SPONSOR_USER_ID=" + syntheticSponsorUserID,
}

func TestProductionComposeVerifierSyntax(t *testing.T) {
	cmd := exec.Command("sh", "-n", productionVerifier)
	cmd.Dir = filepath.Clean(filepath.Join(".."))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n %s: %v\n%s", productionVerifier, err, output)
	}
}

func TestProductionComposeRenderedTextInspectorFailsClosed(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join(".."))
	inspector := filepath.Join(repoRoot, productionTextInspector)

	if output, err := exec.Command("sh", "-n", inspector).CombinedOutput(); err != nil {
		t.Fatalf("sh -n %s: %v\n%s", productionTextInspector, err, output)
	}

	tests := []struct {
		name       string
		contents   string
		missing    bool
		wantError  bool
		wantOutput string
	}{
		{name: "clean render", contents: "services:\n  app:\n    image: synthetic\n"},
		{name: "legacy volume", contents: "volumes:\n  jobcron_config:\n", wantError: true, wantOutput: "legacy volume"},
		{name: "missing render", missing: true, wantError: true, wantOutput: "rendered Compose inspection"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "must-not-leak-render-path")
			if !test.missing {
				if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
					t.Fatalf("write rendered fixture: %v", err)
				}
			}
			cmd := exec.Command("sh", inspector, path)
			output, err := cmd.CombinedOutput()
			if test.wantError != (err != nil) {
				t.Fatalf("inspector error = %v, wantError %v; output=%q", err, test.wantError, output)
			}
			if test.wantOutput != "" && !strings.Contains(string(output), test.wantOutput) {
				t.Fatalf("inspector output = %q, want %q", output, test.wantOutput)
			}
			if strings.Contains(string(output), path) {
				t.Fatalf("inspector disclosed rendered path: %q", output)
			}
		})
	}

	t.Run("grep failure", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "must-not-leak-render-path")
		if err := os.WriteFile(path, []byte("services:\n"), 0o600); err != nil {
			t.Fatalf("write rendered fixture: %v", err)
		}
		bin := filepath.Join(root, "bin")
		if err := os.Mkdir(bin, 0o700); err != nil {
			t.Fatalf("create fake bin: %v", err)
		}
		fakeGrep := filepath.Join(bin, "grep")
		if err := os.WriteFile(fakeGrep, []byte("#!/bin/sh\nexit 2\n"), 0o700); err != nil {
			t.Fatalf("write fake grep: %v", err)
		}
		cmd := exec.Command("sh", inspector, path)
		cmd.Env = []string{"PATH=" + bin}
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("inspector accepted failed grep: %q", output)
		}
		want := "production Compose validation failed: rendered Compose inspection"
		if got := strings.TrimSpace(string(output)); got != want {
			t.Fatalf("grep failure output = %q, want only %q", got, want)
		}
	})
}

func TestProductionComposeOperatorDocsUseFailClosedInspector(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join(".."))
	for _, name := range []string{
		filepath.Join("deploy", "production", "HUMAN_DEPLOY_GUIDE.md"),
		filepath.Join("docs", "superpowers", "plans", "260715-postgresql-convergence-slice-5-first-production-deployment.md"),
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(contents)
		if strings.Contains(text, "if rg -q 'jobcron_config|/root/.config/jobcron'") {
			t.Errorf("%s retains fail-open rg inspector", name)
		}
		if strings.Contains(text, "docker compose --env-file .env config --quiet") {
			t.Errorf("%s exposes unsuppressed real-.env Compose diagnostics", name)
		}
		if !strings.Contains(text, "sh ../../scripts/inspect-production-compose-render.sh") ||
			!strings.Contains(text, "\"$rendered_compose\"") {
			t.Errorf("%s does not invoke the fail-closed rendered-text inspector", name)
		}
	}
}

func TestProductionDocsUseTransientSSMOperations(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join(".."))
	for _, name := range []string{
		filepath.Join("deploy", "production", "HUMAN_DEPLOY_GUIDE.md"),
		filepath.Join("deploy", "production", "README.md"),
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := strings.ToLower(string(contents))
		for _, stale := range []string{
			"ssh -",
			"docker buildx build",
			"--env-file .env config",
			"existing ec2 `.env`",
			"docker compose up",
			"make rds public",
			"associate-address",
			"cloudflare record",
			"dns record",
			"0.0.0.0:",
		} {
			if strings.Contains(text, stale) {
				t.Errorf("%s retains stale production operation %q", name, stale)
			}
		}
		for _, required := range []string{
			"session manager",
			"/run/jobcron",
			"digest",
			"systemd",
			"private verification",
			"recovery manifest",
			"stop condition",
		} {
			if !strings.Contains(text, required) {
				t.Errorf("%s does not document %q", name, required)
			}
		}
	}
}

func TestProductionMigrationDocsBindReviewedSource(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "deploy", "production", "HUMAN_DEPLOY_GUIDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if strings.Contains(text, "go run ./cmd/jobcron-user migrate") {
		t.Fatal("production guide runs privileged migration from the ambient worktree")
	}
	if strings.Contains(text, "go build -trimpath -o \"$migration_bin\" ./cmd/jobcron-user") {
		t.Fatal("production guide builds privileged migration from the ambient worktree")
	}
	for _, required := range []string{
		"(\nset -eu",
		"PATH=/usr/bin:/bin",
		"JOBCRON_REVIEWED_SHA",
		"JOBCRON_PREVIOUS_IMAGE_COMMIT",
		"GIT_NO_REPLACE_OBJECTS=1",
		"JOBCRON_GO_BINARY",
		"JOBCRON_GO_TOOLCHAIN_DIGEST",
		"computed_go_toolchain_digest",
		"run_git rev-parse HEAD",
		"run_git status --porcelain=v1 --untracked-files=all",
		"run_git stash list",
		"run_git show \"${JOBCRON_REVIEWED_SHA}:scripts/build-reviewed-jobcron-user.sh\"",
		"$JOBCRON_PREVIOUS_IMAGE_COMMIT:internal/storage/postgres_migrations",
		"$JOBCRON_REVIEWED_SHA:internal/storage/postgres_migrations",
		"test \"$previous_migration_tree\" = \"$reviewed_migration_tree\"",
		"--backfill-legacy-migration-tree \"$previous_migration_tree\"",
		"build-reviewed-jobcron-user",
		"\"$JOBCRON_GO_TOOLCHAIN_DIGEST\"",
		"jobcron-user-$JOBCRON_REVIEWED_SHA",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("production guide does not bind migration source with %q", required)
		}
	}
}

func TestProductionSurfacesUseExistingDailyScrapeTimeVariable(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join(".."))
	standaloneInventedName := "JOBCRON_DAILY_" + "TIME"
	for _, name := range []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("deploy", "production", ".env.example"),
		filepath.Join("deploy", "production", "HUMAN_DEPLOY_GUIDE.md"),
		filepath.Join("deploy", "production", "README.md"),
		filepath.Join("deploy", "production", "compose.yaml"),
		filepath.Join("deploy", "production", "compose_test.go"),
		filepath.Join("docs", "superpowers", "plans", "260715-postgresql-convergence-slice-5-first-production-deployment.md"),
		filepath.Join("scripts", "verify-production-compose.sh"),
		filepath.Join("scripts", "verify_production_compose_test.go"),
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(contents), standaloneInventedName) {
			t.Errorf("%s uses invented standalone daily-time variable", name)
		}
	}
}

func TestProductionComposeVerifierAcceptsDigestReference(t *testing.T) {
	result := runProductionVerifier(t, nil, syntheticProductionEnvironment)
	if result.err != nil {
		t.Fatalf("verifier rejected digest image reference: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.output, "production Compose contract verified") {
		t.Fatalf("success output = %q, want contract confirmation", result.output)
	}
}

func TestProductionComposeVerifierRejectsMutableImageReference(t *testing.T) {
	result := runProductionVerifier(
		t,
		nil,
		replaceEnvironment(syntheticProductionEnvironment, "JOBCRON_IMAGE", syntheticCommitTag),
	)
	assertRejectedContract(t, result, "services.app.image must use private GHCR sha256 digest")
	if strings.Contains(result.output, syntheticCommitTag) {
		t.Fatalf("rejection output disclosed candidate image reference: %q", result.output)
	}
}

func TestProductionComposeVerifierRequiresSyntheticInputs(t *testing.T) {
	for _, name := range []string{
		"JOBCRON_IMAGE",
		"DATABASE_URL",
		"SESSION_SECRET",
		"JOBCRON_CREDENTIAL_ENCRYPTION_KEY",
		"JOBCRON_PROXY_SECRET",
		"JOBCRON_SIGNUP_ACCESS_CODE",
		"JOBCRON_STAGE1_SPONSOR_USER_ID",
	} {
		t.Run(name, func(t *testing.T) {
			result := runProductionVerifier(t, nil, removeEnvironment(syntheticProductionEnvironment, name))
			assertRejectedContract(t, result, name)
		})
	}
}

func TestProductionComposeVerifierDoesNotDiscloseInvalidTemporaryDirectory(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join(".."))
	marker := "must-not-leak-tmpdir-marker"
	invalidTemporaryDirectory := filepath.Join(t.TempDir(), marker, "missing")
	cmd := exec.Command("sh", productionVerifier)
	cmd.Dir = repoRoot
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + invalidTemporaryDirectory,
	}, syntheticProductionEnvironment...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("verifier accepted invalid TMPDIR %q", invalidTemporaryDirectory)
	}
	want := "production Compose contract failed: private rendered Compose file"
	if got := strings.TrimSpace(string(output)); got != want {
		t.Fatalf("invalid TMPDIR output = %q, want only %q", got, want)
	}
	if strings.Contains(string(output), marker) || strings.Contains(string(output), invalidTemporaryDirectory) {
		t.Fatalf("invalid TMPDIR output disclosed environment-controlled path: %q", output)
	}
}

func TestProductionComposeVerifierRejectsUnsafeTopology(t *testing.T) {
	tests := []struct {
		name     string
		contract string
		mutate   func(string) string
	}{
		{
			name:     "app filesystem mount",
			contract: "services.app.volumes",
			mutate: replaceOnce(
				"    expose:\n      - \"7777\"",
				"    volumes:\n      - ./state:/root/.config/jobcron\n    expose:\n      - \"7777\"",
			),
		},
		{
			name:     "app omits loopback port",
			contract: "services.app.ports",
			mutate: replaceOnce(
				"    ports:\n      - target: 7777\n        published: \"7777\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
				"    ports: []",
			),
		},
		{
			name:     "app publishes on all interfaces",
			contract: "services.app.ports",
			mutate: replaceOnce(
				"    ports:\n      - target: 7777\n        published: \"7777\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
				"    ports:\n      - target: 7777\n        published: \"7777\"\n        host_ip: 0.0.0.0\n        protocol: tcp",
			),
		},
		{
			name:     "app publishes Caddy HTTP port",
			contract: "services.app.ports",
			mutate: replaceOnce(
				"    ports:\n      - target: 7777\n        published: \"7777\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
				"    ports:\n      - target: 7777\n        published: \"80\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
			),
		},
		{
			name:     "Caddy omits private TLS port",
			contract: "services.caddy.ports",
			mutate: replaceOnce(
				"    ports:\n      - target: 443\n        published: \"8443\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
				"    ports: []",
			),
		},
		{
			name:     "Caddy publishes on all interfaces",
			contract: "services.caddy.ports",
			mutate: replaceOnce(
				"    ports:\n      - target: 443\n        published: \"8443\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
				"    ports:\n      - target: 443\n        published: \"8443\"\n        host_ip: 0.0.0.0\n        protocol: tcp",
			),
		},
		{
			name:     "Caddy publishes an extra port",
			contract: "services.caddy.ports",
			mutate: replaceOnce(
				"    ports:\n      - target: 443\n        published: \"8443\"\n        host_ip: 127.0.0.1\n        protocol: tcp",
				"    ports:\n      - target: 443\n        published: \"8443\"\n        host_ip: 127.0.0.1\n        protocol: tcp\n      - \"8080:8080\"",
			),
		},
		{
			name:     "runtime network is externally routable",
			contract: "networks",
			mutate:   replaceOnce("    internal: true", "    internal: false"),
		},
		{
			name:     "app omits outbound network",
			contract: "networks",
			mutate: replaceOnce(
				"    networks:\n      - runtime\n      - outbound",
				"    networks:\n      - runtime",
			),
		},
		{
			name:     "Caddy joins outbound network",
			contract: "networks",
			mutate: replaceOnce(
				"    networks:\n      - runtime\n    logging:",
				"    networks:\n      - runtime\n      - outbound\n    logging:",
			),
		},
		{
			name:     "outbound network is not a bridge",
			contract: "networks",
			mutate:   replaceOnce("    driver: bridge", "    driver: overlay"),
		},
		{
			name:     "Caddy omits transient certificate mount",
			contract: "services.caddy.volumes",
			mutate:   replaceOnce("      - /run/jobcron/caddy:/run/jobcron/caddy:ro\n", ""),
		},
		{
			name:     "legacy app volume remains declared",
			contract: "volumes.jobcron_config",
			mutate: replaceOnce(
				"volumes:\n  caddy_data:\n  caddy_config:",
				"volumes:\n  caddy_data:\n  caddy_config:\n  jobcron_config:",
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runProductionVerifier(t, test.mutate, syntheticProductionEnvironment)
			assertRejectedContract(t, result, test.contract)
		})
	}
}

func TestProductionComposeVerifierRejectsMissingAppEnvironment(t *testing.T) {
	for _, name := range []string{
		"DATABASE_URL",
		"SESSION_SECRET",
		"JOBCRON_CREDENTIAL_ENCRYPTION_KEY",
		"JOBCRON_ENV",
		"JOBCRON_HOST",
		"JOBCRON_PORT",
		"JOBCRON_NO_OPEN",
		"AWS_EC2_METADATA_DISABLED",
		"JOBCRON_SCHEDULER_ENABLED",
		"JOBCRON_DAILY_SCRAPE_TIME",
		"JOBCRON_SIGNUP_ACCESS_CODE",
		"JOBCRON_STAGE1_SPONSOR_USER_ID",
	} {
		t.Run(name, func(t *testing.T) {
			result := runProductionVerifier(t, removeComposeEnvironment(name), syntheticProductionEnvironment)
			assertRejectedContract(t, result, "services.app.environment."+name)
		})
	}
	t.Run("Caddy AWS_EC2_METADATA_DISABLED", func(t *testing.T) {
		result := runProductionVerifier(t, replaceOnce(
			"    environment:\n      JOBCRON_PROXY_SECRET: \"${JOBCRON_PROXY_SECRET:?set JOBCRON_PROXY_SECRET in .env}\"\n      AWS_EC2_METADATA_DISABLED: \"true\"",
			"    environment:\n      JOBCRON_PROXY_SECRET: \"${JOBCRON_PROXY_SECRET:?set JOBCRON_PROXY_SECRET in .env}\"",
		), syntheticProductionEnvironment)
		assertRejectedContract(t, result, "services.caddy.environment.AWS_EC2_METADATA_DISABLED")
	})
	t.Run("JOBCRON_PROXY_SECRET", func(t *testing.T) {
		result := runProductionVerifier(t, replaceOnce(
			"      JOBCRON_STAGE1_SPONSOR_USER_ID: >-\n        ${JOBCRON_STAGE1_SPONSOR_USER_ID:?set JOBCRON_STAGE1_SPONSOR_USER_ID in .env}\n      JOBCRON_PROXY_SECRET: \"${JOBCRON_PROXY_SECRET:?set JOBCRON_PROXY_SECRET in .env}\"",
			"      JOBCRON_STAGE1_SPONSOR_USER_ID: >-\n        ${JOBCRON_STAGE1_SPONSOR_USER_ID:?set JOBCRON_STAGE1_SPONSOR_USER_ID in .env}",
		), syntheticProductionEnvironment)
		assertRejectedContract(t, result, "services.app.environment.JOBCRON_PROXY_SECRET")
	})
}

func TestProductionComposeVerifierRejectsMismatchedSensitiveEnvironment(t *testing.T) {
	tests := []struct {
		name          string
		mismatchValue string
		mutate        func(string) string
	}{
		{
			name:          "DATABASE_URL",
			mismatchValue: "postgres://mismatch.invalid/jobcron",
			mutate: replaceOnce(
				`      DATABASE_URL: "${DATABASE_URL:?set DATABASE_URL in .env}"`,
				`      DATABASE_URL: "postgres://mismatch.invalid/jobcron"`,
			),
		},
		{
			name:          "SESSION_SECRET",
			mismatchValue: "mismatched-session-value",
			mutate: replaceOnce(
				`      SESSION_SECRET: "${SESSION_SECRET:?set SESSION_SECRET in .env}"`,
				`      SESSION_SECRET: "mismatched-session-value"`,
			),
		},
		{
			name:          "JOBCRON_CREDENTIAL_ENCRYPTION_KEY",
			mismatchValue: "mismatched-credential-key",
			mutate: replaceOnce(
				"      JOBCRON_CREDENTIAL_ENCRYPTION_KEY: >-\n        ${JOBCRON_CREDENTIAL_ENCRYPTION_KEY:?set JOBCRON_CREDENTIAL_ENCRYPTION_KEY in .env}",
				`      JOBCRON_CREDENTIAL_ENCRYPTION_KEY: "mismatched-credential-key"`,
			),
		},
		{
			name:          "JOBCRON_PROXY_SECRET",
			mismatchValue: "mismatched-proxy-secret",
			mutate: replaceOnce(
				"      JOBCRON_STAGE1_SPONSOR_USER_ID: >-\n        ${JOBCRON_STAGE1_SPONSOR_USER_ID:?set JOBCRON_STAGE1_SPONSOR_USER_ID in .env}\n      JOBCRON_PROXY_SECRET: \"${JOBCRON_PROXY_SECRET:?set JOBCRON_PROXY_SECRET in .env}\"",
				"      JOBCRON_STAGE1_SPONSOR_USER_ID: >-\n        ${JOBCRON_STAGE1_SPONSOR_USER_ID:?set JOBCRON_STAGE1_SPONSOR_USER_ID in .env}\n      JOBCRON_PROXY_SECRET: \"mismatched-proxy-secret\"",
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runProductionVerifier(t, test.mutate, syntheticProductionEnvironment)
			assertRejectedContract(t, result, "services.app.environment."+test.name)
			if strings.Contains(result.output, test.mismatchValue) {
				t.Fatalf("rejection output disclosed mismatched %s value: %q", test.name, result.output)
			}
		})
	}
	t.Run("Caddy JOBCRON_PROXY_SECRET", func(t *testing.T) {
		mismatchValue := "mismatched-caddy-proxy-secret"
		result := runProductionVerifier(t, replaceOnce(
			"    environment:\n      JOBCRON_PROXY_SECRET: \"${JOBCRON_PROXY_SECRET:?set JOBCRON_PROXY_SECRET in .env}\"",
			"    environment:\n      JOBCRON_PROXY_SECRET: \""+mismatchValue+"\"",
		), syntheticProductionEnvironment)
		assertRejectedContract(t, result, "services.caddy.environment.JOBCRON_PROXY_SECRET")
		if strings.Contains(result.output, mismatchValue) {
			t.Fatalf("rejection output disclosed mismatched Caddy proxy secret: %q", result.output)
		}
	})
}

func TestProductionComposeVerifierRejectsWrongProductionSettings(t *testing.T) {
	tests := []struct {
		name     string
		contract string
		mutate   func(string) string
	}{
		{
			name:     "registry pull during compose start",
			contract: "services.app.pull_policy",
			mutate:   replaceOnce("    pull_policy: never", "    pull_policy: always"),
		},
		{
			name:     "non-production mode",
			contract: "services.app.environment.JOBCRON_ENV",
			mutate:   replaceOnce("      JOBCRON_ENV: production", "      JOBCRON_ENV: development"),
		},
		{
			name:     "non-public app bind address",
			contract: "services.app.environment.JOBCRON_HOST",
			mutate:   replaceOnce("      JOBCRON_HOST: 0.0.0.0", "      JOBCRON_HOST: 127.0.0.1"),
		},
		{
			name:     "unexpected app port",
			contract: "services.app.environment.JOBCRON_PORT",
			mutate:   replaceOnce("      JOBCRON_PORT: \"7777\"", "      JOBCRON_PORT: \"8888\""),
		},
		{
			name:     "browser opening enabled",
			contract: "services.app.environment.JOBCRON_NO_OPEN",
			mutate:   replaceOnce("      JOBCRON_NO_OPEN: \"1\"", "      JOBCRON_NO_OPEN: \"0\""),
		},
		{
			name:     "disabled scheduler",
			contract: "services.app.environment.JOBCRON_SCHEDULER_ENABLED",
			mutate:   replaceOnce("      JOBCRON_SCHEDULER_ENABLED: \"1\"", "      JOBCRON_SCHEDULER_ENABLED: \"0\""),
		},
		{
			name:     "hardcoded scrape time",
			contract: "services.app.environment.JOBCRON_DAILY_SCRAPE_TIME",
			mutate:   replaceOnce(`      JOBCRON_DAILY_SCRAPE_TIME: "${JOBCRON_DAILY_SCRAPE_TIME:-05:00}"`, `      JOBCRON_DAILY_SCRAPE_TIME: "05:00"`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runProductionVerifier(t, test.mutate, syntheticProductionEnvironment)
			assertRejectedContract(t, result, test.contract)
		})
	}
}

func TestProductionComposeVerifierRejectsCommandOverrides(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) string
	}{
		{name: "port", mutate: replaceOnce("      - --port\n      - \"7777\"", "      - --port\n      - \"8888\"")},
		{name: "host", mutate: replaceOnce("      - 0.0.0.0", "      - 127.0.0.1")},
		{name: "demo", mutate: replaceOnce("      - --no-open", "      - --no-open\n      - --demo")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runProductionVerifier(t, test.mutate, syntheticProductionEnvironment)
			assertRejectedContract(t, result, "services.app.command")
		})
	}
}

func TestProductionComposeVerifierRejectsDifferentImmutableCandidate(t *testing.T) {
	result := runProductionVerifier(t, replaceOnce(
		`    image: "${JOBCRON_IMAGE:?set JOBCRON_IMAGE in .env}"`,
		`    image: "example.invalid/jobcron:sha-fedcba987654"`,
	), syntheticProductionEnvironment)
	assertRejectedContract(t, result, "services.app.image must equal JOBCRON_IMAGE")
}

func TestProductionComposeVerifierDailyTimeDefaultAndPreservation(t *testing.T) {
	result := runProductionVerifier(t, nil, syntheticProductionEnvironment)
	if result.err != nil {
		t.Fatalf("verifier rejected preserved daily time: %v\n%s", result.err, result.output)
	}

	result = runProductionVerifier(t, nil, removeEnvironment(syntheticProductionEnvironment, "JOBCRON_DAILY_SCRAPE_TIME"))
	if result.err != nil {
		t.Fatalf("verifier rejected safe daily-time default: %v\n%s", result.err, result.output)
	}

	result = runProductionVerifier(t, replaceOnce(
		`${JOBCRON_DAILY_SCRAPE_TIME:-05:00}`,
		`${JOBCRON_DAILY_SCRAPE_TIME:-04:00}`,
	), removeEnvironment(syntheticProductionEnvironment, "JOBCRON_DAILY_SCRAPE_TIME"))
	assertRejectedContract(t, result, "services.app.environment.JOBCRON_DAILY_SCRAPE_TIME")
}

func TestProductionComposeVerifierRejectsForbiddenAppEnvironment(t *testing.T) {
	for _, name := range []string{
		"JOBCRON_DEMO",
		"JOBCRON_ADMIN_TOKEN",
		"JOBCRON_WORKNET_KEY",
	} {
		t.Run(name, func(t *testing.T) {
			forbiddenValue := "forbidden-synthetic-value-" + name
			result := runProductionVerifier(t, replaceOnce(
				"      JOBCRON_NO_OPEN: \"1\"",
				"      JOBCRON_NO_OPEN: \"1\"\n      "+name+": \""+forbiddenValue+"\"",
			), syntheticProductionEnvironment)
			assertRejectedContract(t, result, "services.app.environment."+name)
			if strings.Contains(result.output, forbiddenValue) {
				t.Fatalf("rejection output disclosed forbidden %s value: %q", name, result.output)
			}
		})
	}
}

func TestProductionComposeVerifierRejectsMutableOrMalformedImage(t *testing.T) {
	for _, image := range []string{
		"example.invalid/jobcron:latest",
		"example.invalid/jobcron:sha-0123456789a",
		"example.invalid/jobcron:sha-0123456789ag",
		"example.invalid/jobcron@sha256:" + strings.Repeat("a", 63),
	} {
		result := runProductionVerifier(t, nil, replaceEnvironment(syntheticProductionEnvironment, "JOBCRON_IMAGE", image))
		assertRejectedContract(t, result, "services.app.image")
	}
}

func TestProductionComposeCIUsesSyntheticInputs(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		t.Fatalf("parse CI workflow: %v", err)
	}

	var contractStep *struct {
		Name string            `yaml:"name"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
	}
	for index := range workflow.Jobs["test"].Steps {
		step := &workflow.Jobs["test"].Steps[index]
		if step.Name == "production Compose contract" {
			contractStep = step
			break
		}
	}
	if contractStep == nil {
		t.Fatal("CI test job is missing the production Compose contract step")
	}
	for _, command := range []string{
		"sh -n scripts/verify-production-compose.sh",
		"sh -n scripts/inspect-production-compose-render.sh",
		"go test ./scripts -run ProductionCompose -count=1",
		"sh scripts/verify-production-compose.sh",
	} {
		if !strings.Contains(contractStep.Run, command) {
			t.Errorf("production Compose CI step is missing %q", command)
		}
	}

	wantEnvironment := map[string]string{
		"JOBCRON_IMAGE":                     syntheticImage,
		"DATABASE_URL":                      syntheticDatabase,
		"SESSION_SECRET":                    syntheticSession,
		"JOBCRON_CREDENTIAL_ENCRYPTION_KEY": syntheticKey,
		"JOBCRON_PROXY_SECRET":              syntheticProxySecret,
		"JOBCRON_DAILY_SCRAPE_TIME":         syntheticDailyTime,
		"JOBCRON_SIGNUP_ACCESS_CODE":        syntheticSignupCode,
		"JOBCRON_STAGE1_SPONSOR_USER_ID":    syntheticSponsorUserID,
	}
	if len(contractStep.Env) != len(wantEnvironment) {
		t.Fatalf("production Compose CI environment has %d entries, want %d synthetic inputs", len(contractStep.Env), len(wantEnvironment))
	}
	for name, want := range wantEnvironment {
		if got := contractStep.Env[name]; got != want {
			t.Errorf("production Compose CI %s = %q, want documented synthetic placeholder", name, got)
		}
	}
}

type verifierResult struct {
	output string
	err    error
}

func runProductionVerifier(t *testing.T, mutate func(string) string, environment []string) verifierResult {
	t.Helper()
	repoRoot := filepath.Clean(filepath.Join(".."))
	root := t.TempDir()
	for _, directory := range []string{"scripts", filepath.Join("deploy", "production"), "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatalf("create fixture directory %s: %v", directory, err)
		}
	}

	copyFixtureFile(t, filepath.Join(repoRoot, productionVerifier), filepath.Join(root, productionVerifier), nil)
	copyFixtureFile(t,
		filepath.Join(repoRoot, "deploy", "production", "compose.yaml"),
		filepath.Join(root, "deploy", "production", "compose.yaml"),
		mutate,
	)
	copyFixtureFile(t,
		filepath.Join(repoRoot, "deploy", "production", "Caddyfile"),
		filepath.Join(root, "deploy", "production", "Caddyfile"),
		nil,
	)

	cmd := exec.Command("sh", productionVerifier)
	cmd.Dir = root
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
	}, environment...)
	output, err := cmd.CombinedOutput()
	assertNoRenderedComposeRemains(t, filepath.Join(root, "tmp"))
	return verifierResult{output: string(output), err: err}
}

func copyFixtureFile(t *testing.T, source, destination string, mutate func(string) string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture %s: %v", source, err)
	}
	text := string(contents)
	if mutate != nil {
		text = mutate(text)
	}
	if err := os.WriteFile(destination, []byte(text), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", destination, err)
	}
}

func assertRejectedContract(t *testing.T, result verifierResult, contract string) {
	t.Helper()
	if result.err == nil {
		t.Fatalf("verifier accepted broken %s contract:\n%s", contract, result.output)
	}
	if !strings.Contains(result.output, contract) {
		t.Fatalf("rejection output = %q, want contract field %s", result.output, contract)
	}
	for _, value := range []string{
		syntheticImage,
		syntheticDatabase,
		syntheticSession,
		syntheticKey,
		syntheticProxySecret,
		syntheticDailyTime,
		syntheticSignupCode,
		syntheticSponsorUserID,
	} {
		if strings.Contains(result.output, value) {
			t.Fatalf("rejection output disclosed a synthetic environment value: %q", result.output)
		}
	}
}

func assertNoRenderedComposeRemains(t *testing.T, temporaryDirectory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(temporaryDirectory, "jobcron-production-compose.*"))
	if err != nil {
		t.Fatalf("glob rendered Compose files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("rendered Compose file was not removed: %v", matches)
	}
}

func replaceOnce(old, new string) func(string) string {
	return func(contents string) string {
		if strings.Count(contents, old) != 1 {
			panic("fixture marker must occur exactly once: " + old)
		}
		return strings.Replace(contents, old, new, 1)
	}
}

func removeComposeEnvironment(name string) func(string) string {
	return func(contents string) string {
		lines := strings.Split(contents, "\n")
		result := make([]string, 0, len(lines))
		removed := false
		for index := 0; index < len(lines); index++ {
			if strings.HasPrefix(lines[index], "      "+name+":") {
				removed = true
				if strings.HasSuffix(lines[index], ">-") && index+1 < len(lines) {
					index++
				}
				continue
			}
			result = append(result, lines[index])
		}
		if !removed {
			panic("environment fixture not found: " + name)
		}
		return strings.Join(result, "\n")
	}
}

func removeEnvironment(environment []string, name string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, name+"=") {
			result = append(result, entry)
		}
	}
	return result
}

func replaceEnvironment(environment []string, name, value string) []string {
	return append(removeEnvironment(environment, name), name+"="+value)
}
