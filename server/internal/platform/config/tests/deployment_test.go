package config_test

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/config"
)

// Every name config.Load reads. Cleared before each case so a developer's own
// shell -- which has all of these set, from the repo's .env -- cannot make a
// deployment look bootable when it is not.
var configuredBy = slices.Concat(aiVariables, emailVariables, hostVariables, []string{
	"API_PORT", "APP_ENV", "CLIENT_IP_HEADER", "CORS_ALLOWED_ORIGINS", "DATABASE_URL",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URI", "JWT_SIGNING_KEY",
	"JOIN_CODE_KEY", "JOIN_CODE_KEY_PREVIOUS",
	"MAX_CONCURRENT_PASSWORD_HASHES", "REFRESH_COOKIE_SECURE", "S3_ACCESS_KEY_ID",
	"S3_BUCKET", "S3_ENDPOINT", "S3_FORCE_PATH_STYLE", "S3_REGION",
	"S3_SECRET_ACCESS_KEY", "VITE_GOOGLE_CLIENT_ID", "DOCS_PUBLIC",
	"IMPORT_S3_BUCKET", "IMPORT_WORK_DIR", "IMPORT_LEGACY_DOC", "IMPORT_PROCESSING_ENABLED", "IMPORT_WORKER_WAKE_URL",
	"IMPORT_WORKER_WAKE_ADDR", "IMPORT_WORKER_IDLE_POLL", "IMPORT_DOCKER_BINARY", "IMPORT_CONVERTER_IMAGE",
	"IMPORT_ACTOR_COUNT", "IMPORT_GLOBAL_COUNT", "IMPORT_SOURCES_PER_ITEM", "IMPORT_ACTOR_MIB",
	"IMPORT_GLOBAL_MIB", "MEDIA_OWNER_QUOTA_MIB", "MEDIA_RETENTION_SWEEP",
})

// What the box's app.secrets.env supplies, by name (Quizzivy/infra
// vps/secrets/app.secrets.env.example). Values are placeholders: this asks
// whether the NAMES add up to a config Load accepts, which is the half that a
// committed file can be wrong about.
var productionSecrets = map[string]string{
	"DATABASE_URL":         "postgres://quizzivy_app:pw@postgres:5432/quizzivy?sslmode=disable",
	"JWT_SIGNING_KEY":      "0123456789abcdef0123456789abcdef0123456789abcdef",
	"JOIN_CODE_KEY":        "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	"GOOGLE_CLIENT_SECRET": "placeholder-secret",
	"S3_ENDPOINT":          "https://account.r2.cloudflarestorage.com",
	"S3_BUCKET":            "quizzivy-media",
	"S3_ACCESS_KEY_ID":     "placeholder-key-id",
	"S3_SECRET_ACCESS_KEY": "placeholder-secret-key",
	"RESEND_API_KEY":       "re_placeholder",
	"SECRETS_KEY":          "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
}

var envLine = regexp.MustCompile(`^([A-Z0-9_]+)=(?:"([^"]*)"|'([^']*)'|([^\s#"']*))\s*(?:#.*)?$`)

// productionEnv parses the committed deploy/production.env.
//
// Hand-parsed rather than pulling in a dotenv reader: the file is a flat list
// of KEY=value lines that this repo writes itself, and a dependency added for a
// test needs a better reason than a few lines.
func productionEnv(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(repoFile(t, "deploy/production.env"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m := envLine.FindStringSubmatch(trimmed)
		if m == nil {
			t.Fatalf("deploy/production.env line %q is not a KEY=value this test can read", trimmed)
		}
		out[m[1]] = m[2] + m[3] + m[4]
	}
	if len(out) == 0 {
		t.Fatal("deploy/production.env has no entries; this test is not reading what it thinks it is")
	}
	return out
}

func repoFile(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

func apply(t *testing.T, sets ...map[string]string) {
	t.Helper()
	for _, name := range configuredBy {
		t.Setenv(name, "")
	}
	for _, set := range sets {
		for name, value := range set {
			t.Setenv(name, value)
		}
	}
}

// The production deployment, assembled from the two places its configuration
// actually lives, and asked the only question that matters: does it boot.
func TestTheCommittedProductionConfigBootsWithTheDocumentedSecrets(t *testing.T) {
	apply(t, productionEnv(t), productionSecrets)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("deploy/production.env plus the box's secrets does not boot: %v", err)
	}
	if !cfg.MediaEnabled() {
		t.Error("object storage is not configured; audio upload would be dead in production")
	}
	if len(cfg.GoogleRedirectURIs) == 0 {
		t.Error("google sign-in is off; §5.3 is not optional in production")
	}
	if cfg.DocsPublic {
		t.Error("the API reference is open to anyone; production must keep the docs gate on")
	}
	if len(cfg.Limits) != 0 {
		t.Errorf("deploy/production.env overrides the usage limits %v; production takes them from Settings → Plan & usage, whose defaults are the hosting plans (CR-002)", cfg.Limits)
	}
}

func dockerfileShipsTheWorker(t *testing.T) bool {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(repoFile(t, "Dockerfile"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			lines = append(lines, trimmed)
		}
	}
	runtime := 0
	for i, line := range lines {
		if strings.HasPrefix(strings.ToUpper(line), "FROM ") {
			runtime = i
		}
	}
	built, copied := false, false
	for i, line := range lines {
		if i < runtime && strings.Contains(line, "./cmd/import-worker") {
			built = true
		}
		if i > runtime && strings.HasPrefix(strings.ToUpper(line), "COPY ") && strings.Contains(line, "/app/import-worker") {
			copied = true
		}
	}
	return built && copied
}

func TestProductionProcessesWordImportsWithAWorkerTheImageShips(t *testing.T) {
	apply(t, productionEnv(t), productionSecrets)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImportProcessing && !dockerfileShipsTheWorker(t) {
		t.Fatal("deploy/production.env turns import processing on, but the Dockerfile does not build /app/import-worker and copy it there")
	}
	if cfg.ImportProcessing {
		assertTheWorkerCanBeWoken(t)
	}
	if cfg.ImportLegacyDoc {
		t.Fatal("production accepts .doc uploads, but the converter .doc needs is a Docker container, and the production image has no Docker daemon")
	}
}

// The API and the worker are two containers on the box's network: the worker
// must listen beyond its own loopback, and the API must wake it on that port.
func assertTheWorkerCanBeWoken(t *testing.T) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	apply(t, productionEnv(t), productionSecrets)
	worker, err := config.LoadImportWorker()
	if err != nil {
		t.Fatalf("the worker process does not boot on deploy/production.env: %v", err)
	}
	host, port, _ := net.SplitHostPort(worker.WakeAddress)
	if host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" {
		t.Fatalf("the worker listens on %s, where the API container cannot reach it", worker.WakeAddress)
	}
	if wake, err := url.Parse(cfg.ImportWorkerWakeURL); err != nil || wake.Port() != port || wake.Hostname() == "localhost" || strings.HasPrefix(wake.Hostname(), "127.") {
		t.Fatalf("the API wakes %q but the worker listens on %s", cfg.ImportWorkerWakeURL, worker.WakeAddress)
	}
	if worker.IdlePoll < time.Hour {
		t.Fatalf("IMPORT_WORKER_IDLE_POLL=%v: the API wakes the worker, so polling is only the fallback; use hours in production", worker.IdlePoll)
	}
}

func TestImportsWithoutObjectStorageRefuseToBoot(t *testing.T) {
	secrets := map[string]string{}
	for name, value := range productionSecrets {
		if !strings.HasPrefix(name, "S3_") {
			secrets[name] = value
		}
	}
	apply(t, productionEnv(t), secrets)
	if _, err := config.Load(); err == nil {
		t.Fatal("Load accepted import storage without the S3_* credentials it reuses")
	}
}

func TestPublicDocsAreRefusedInProduction(t *testing.T) {
	apply(t, productionEnv(t), productionSecrets, map[string]string{"DOCS_PUBLIC": "true"})

	if _, err := config.Load(); err == nil {
		t.Fatal("Load accepted DOCS_PUBLIC=true with APP_ENV=production")
	}
}

// Proves the test above bites. Drop the one value that was missing and Load must
// refuse, rather than starting with Google half configured.
func TestRemovingTheClientIdIsRefusedRatherThanIgnored(t *testing.T) {
	env := productionEnv(t)
	delete(env, "GOOGLE_CLIENT_ID")
	apply(t, env, productionSecrets)

	if _, err := config.Load(); err == nil {
		t.Fatal("Load accepted Google configured two-of-three; that is the state that crashed production")
	}
}

// The other half of the same failure, and the quieter one: object storage set
// under the R2_* names does not fail, it disables media. docs/setup/dns.md said
// to set exactly those until 2026-09-01.
func TestObjectStorageUnderTheWrongNamesLeavesMediaOff(t *testing.T) {
	secrets := map[string]string{}
	for name, value := range productionSecrets {
		if strings.HasPrefix(name, "S3_") {
			continue
		}
		secrets[name] = value
	}
	env := productionEnv(t)
	for name := range env {
		if strings.HasPrefix(name, "IMPORT_") {
			delete(env, name)
		}
	}
	apply(t, env, secrets, map[string]string{
		"R2_ACCOUNT_ID": "account", "R2_ACCESS_KEY_ID": "key", "R2_SECRET_ACCESS_KEY": "secret",
	})

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("R2_* names should be ignored, not fatal: %v", err)
	}
	if cfg.MediaEnabled() {
		t.Fatal("R2_* names now reach the S3_* config; update the docs and delete this test")
	}
}

// The api job of deploy.yml, which deploys production to the VPS.
// Only a release runs it, so this reads the file as text and asks the things
// that would go wrong without anyone noticing: a deploy that trusts any host
// key, one that reaches the box before the image exists, a push from the
// runner's own image store, and an SDK promote that no longer waits for the
// API job that ran.
func TestTheVPSDeployJobPushesBeforeItDeploysAndChecksTheHostKey(t *testing.T) {
	text := repoFile(t, ".github/workflows/deploy.yml")
	start := strings.Index(text, "\n  api:")
	end := strings.Index(text, "\n  sdk:")
	if start < 0 || end < start {
		t.Fatal("deploy.yml has no api job; this test is not reading what it thinks it is")
	}
	job := text[start:end]

	if strings.Contains(job, "StrictHostKeyChecking=no") || strings.Contains(job, "StrictHostKeyChecking no") {
		t.Fatal("api turns host key checking off: the host key must come from VPS_KNOWN_HOSTS")
	}
	if !strings.Contains(job, "StrictHostKeyChecking=yes") {
		t.Fatal("api does not require a known host key")
	}
	push := strings.Index(job, `scripts/ci/push-image.sh "$image"`)
	ssh := strings.Index(job, `deploy "$SHA"`)
	if push < 0 || ssh < 0 || push > ssh {
		t.Fatal(`api must run scripts/ci/push-image.sh before it asks the box to deploy "$SHA"`)
	}
	// The first deploy to the VPS pushed with `docker push`, which mounted the
	// distroless layers out of an image the runner ships with, and ghcr.io
	// refused the manifest. The job's comment may name the command; no step may
	// run it.
	for _, line := range strings.Split(job, "\n") {
		if code := strings.TrimSpace(line); !strings.HasPrefix(code, "#") && strings.Contains(code, "docker push") {
			t.Fatalf("api runs %q: push with scripts/ci/push-image.sh, whose header says why", code)
		}
	}

	sdk := text[end:]
	if !strings.Contains(sdk, "needs: [resolve, api]") || !strings.Contains(sdk, "needs.api.result == 'success'") {
		t.Fatal("the sdk job no longer waits for a successful api job: it could promote the SDK before the API that serves it is live")
	}
}
