package build

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func samplePlan() Plan {
	return Plan{
		Service:    "api",
		Image:      "acme-api:sha-abc1234",
		Repository: "acme-api",
		Tag:        "sha-abc1234",
		Platform:   "linux/arm64",
		Dockerfile: "/repo/services/api/Dockerfile",
		Context:    "/repo",
		Labels: []string{
			"org.opencontainers.image.revision=abc1234",
			"org.opencontainers.image.created=2026-08-14T05:42:00Z",
		},
		BuildArgs: []string{"MEIMEI_BUILD_ID=sha-abc1234"},
		Output:    OutputLoad,
	}
}

func TestArgs(t *testing.T) {
	got := strings.Join(samplePlan().Args(), " ")
	want := "buildx build " +
		"--platform linux/arm64 " +
		"--provenance=false --sbom=false " +
		"--file /repo/services/api/Dockerfile " +
		"--label org.opencontainers.image.revision=abc1234 " +
		"--label org.opencontainers.image.created=2026-08-14T05:42:00Z " +
		"--build-arg MEIMEI_BUILD_ID=sha-abc1234 " +
		"--tag acme-api:sha-abc1234 " +
		"--load " +
		"/repo"

	if got != want {
		t.Errorf("Args:\n got %s\nwant %s", got, want)
	}
}

// The context is the last argument, as docker requires. Easy to break by
// appending a flag in the wrong place, and the failure is confusing when it
// happens (docker reads the flag as the context path).
func TestArgsContextIsLast(t *testing.T) {
	args := samplePlan().Args()
	if args[len(args)-1] != "/repo" {
		t.Errorf("last arg = %q, want the build context", args[len(args)-1])
	}
}

// Attestations are off for every build: the docker exporter cannot represent
// them, so a --load build fails outright with them on.
func TestArgsAlwaysDisableAttestations(t *testing.T) {
	for _, out := range []Output{OutputLoad, OutputPush} {
		p := samplePlan()
		p.Output = out
		args := strings.Join(p.Args(), " ")
		if !strings.Contains(args, "--provenance=false") || !strings.Contains(args, "--sbom=false") {
			t.Errorf("Output %v: args = %s, want attestations disabled", out, args)
		}
	}
}

func TestArgsOutputFlag(t *testing.T) {
	p := samplePlan()
	if !contains(p.Args(), "--load") {
		t.Error("a local plan should use --load")
	}
	p.Output = OutputPush
	if !contains(p.Args(), "--push") {
		t.Error("a push plan should use --push")
	}
	if contains(p.Args(), "--load") {
		t.Error("a push plan should not also load")
	}
}

func TestString(t *testing.T) {
	s := samplePlan().String()
	if !strings.HasPrefix(s, "docker buildx build ") {
		t.Errorf("String() = %q, want a runnable docker command", s)
	}
}

// Run streams to the writers it is given and reports the exit code, which is
// the only thing it treats as the verdict.
func TestRunStreamsAndReportsExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub is unix-only")
	}
	dir := t.TempDir()

	stub := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"out: $*\"\necho 'err line' >&2\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	r := Runner{Docker: stub, Stdout: &out, Stderr: &errOut}
	if err := r.Run(context.Background(), samplePlan()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(out.String(), "buildx build") {
		t.Errorf("stdout = %q, want the forwarded arguments", out.String())
	}
	if !strings.Contains(errOut.String(), "err line") {
		t.Errorf("stderr = %q, want buildx's progress stream", errOut.String())
	}
}

func TestRunReportsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub is unix-only")
	}
	stub := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	r := Runner{Docker: stub, Stdout: &out, Stderr: &errOut}
	err := r.Run(context.Background(), samplePlan())
	if err == nil {
		t.Fatal("want an error for a non-zero exit")
	}
	// The service has to be named: with several builds in a run, "exit status
	// 3" on its own says nothing about which one.
	if !strings.Contains(err.Error(), "api") {
		t.Errorf("error = %q, want it to name the service", err)
	}
}

func TestCheckDockerMissingBinary(t *testing.T) {
	err := CheckDocker(context.Background(), filepath.Join(t.TempDir(), "not-here"))
	if err == nil {
		t.Fatal("want an error for a missing docker")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error = %q, want it to mention PATH", err)
	}
}

func TestHostPlatformFallsBackToThisMachine(t *testing.T) {
	// A docker that cannot run at all forces the fallback path.
	got := HostPlatform(context.Background(), filepath.Join(t.TempDir(), "not-here"))
	want := "linux/" + goArchToDocker(runtime.GOARCH)
	if got != want {
		t.Errorf("HostPlatform = %q, want the fallback %q", got, want)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func runtimeGOARCH() string { return runtime.GOARCH }

// WriteAuth must produce a config docker understands, and must not touch the
// user's own — logging in would replace whatever registry session was there.
func TestWriteAuth(t *testing.T) {
	dir, cleanup, err := WriteAuth("reg.example.com", "AWS", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	body, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Auths map[string]struct{ Auth string } `json:"auths"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}
	got, ok := cfg.Auths["reg.example.com"]
	if !ok {
		t.Fatalf("no entry for the registry: %s", body)
	}
	dec, err := base64.StdEncoding.DecodeString(got.Auth)
	if err != nil || string(dec) != "AWS:secret" {
		t.Errorf("auth = %q (%v), want AWS:secret", dec, err)
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("cleanup should remove the credential directory")
	}
}

// The credential directory reaches docker as DOCKER_CONFIG, which is what
// keeps the push out of the user's own configuration.
func TestRunnerPassesDockerConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub is unix-only")
	}
	stub := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$DOCKER_CONFIG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	r := Runner{Docker: stub, Stdout: &out, Stderr: &out, ConfigDir: "/tmp/creds"}
	if err := r.Push(context.Background(), "img"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/tmp/creds") {
		t.Errorf("output = %q, want DOCKER_CONFIG forwarded", out.String())
	}
}
