package build

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultDocker is the binary the runner shells out to.
const DefaultDocker = "docker"

// Args is the docker command line for a plan, less the binary itself.
//
// Split out from running it so the command can be asserted in a test without a
// docker daemon — the arguments are where the decisions end up, and they are
// the thing worth pinning down.
func (p Plan) Args() []string {
	args := []string{
		"buildx", "build",
		"--platform", p.Platform,
		// buildx attaches provenance and SBOM manifests by default. They are
		// untagged children of the image, which the docker exporter cannot
		// represent at all (a --load build fails outright) and which a
		// registry's untagged-image lifecycle rule would later sweep.
		"--provenance=false",
		"--sbom=false",
		"--file", p.Dockerfile,
	}
	for _, l := range p.Labels {
		args = append(args, "--label", l)
	}
	args = append(args, "--tag", p.Image)

	// A push over the CLI route still builds with --load: the image goes to the
	// local store first and is uploaded separately.
	if p.Output == OutputPush && p.Route == RouteBuildkit {
		args = append(args, "--push")
	} else {
		args = append(args, "--load")
	}

	return append(args, p.Context)
}

// String renders the command as it would be typed, for --dry-run and for
// putting in an error message.
func (p Plan) String() string {
	parts := append([]string{DefaultDocker}, p.Args()...)
	return strings.Join(parts, " ")
}

// Runner executes plans by shelling out to the docker CLI.
//
// Shelling out rather than driving buildkit directly: buildx resolves the
// Dockerfile frontend (every Dockerfile here opens `# syntax=docker/dockerfile:1`,
// which is a frontend image buildx fetches and runs), handles registry auth,
// assembles multi-platform manifests and manages exporters. Reimplementing that
// against the buildkit client would mean owning all of it permanently, to gain
// progress reporting that buildx can already emit as JSON.
type Runner struct {
	// Docker is the binary to invoke. Empty means DefaultDocker.
	Docker string

	// Stdout and Stderr receive the build's output as it happens. buildx writes
	// its progress to stderr.
	Stdout io.Writer
	Stderr io.Writer

	// ConfigDir is a DOCKER_CONFIG directory carrying registry credentials for
	// this run. Empty uses the user's own docker configuration.
	ConfigDir string
}

// Run executes one plan, streaming output as it goes.
//
// Success is the exit code, and only the exit code. Progress output is for the
// reader, never for deciding whether the build worked — that way a change in
// how buildx formats its progress can never turn a good build into a failure.
func (r Runner) Run(ctx context.Context, p Plan) error {
	cmd := r.command(ctx, p.Args()...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s: %w", p.Service, err)
	}
	return nil
}

// CheckDocker reports whether the docker CLI and buildx are usable, so that a
// missing dependency is named up front instead of surfacing as a failed build.
func CheckDocker(ctx context.Context, bin string) error {
	if bin == "" {
		bin = DefaultDocker
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%q not found on PATH", bin)
	}
	if err := exec.CommandContext(ctx, bin, "buildx", "version").Run(); err != nil {
		return fmt.Errorf("%q is present but `%s buildx version` failed — is the buildx plugin installed?", bin, bin)
	}
	return nil
}

// HostPlatform is the platform a local build should target.
//
// It asks the docker daemon rather than assuming this process's architecture,
// because the daemon is what will run the image and it need not be on this
// machine. Falling back to the local architecture keeps a build possible when
// the daemon cannot be reached — the build itself will report that better than
// a platform lookup would.
func HostPlatform(ctx context.Context, bin string) string {
	if bin == "" {
		bin = DefaultDocker
	}
	out, err := exec.CommandContext(ctx, bin, "version", "--format", "{{.Server.Arch}}").Output()
	if arch := strings.TrimSpace(string(out)); err == nil && arch != "" {
		return "linux/" + arch
	}
	return "linux/" + goArchToDocker(runtime.GOARCH)
}

// goArchToDocker maps Go's architecture names onto docker's, which agree for
// the platforms in use here but are separate vocabularies.
func goArchToDocker(goarch string) string {
	switch goarch {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return goarch
	}
}

// Route is who uploads a pushed image.
type Route int

const (
	// RouteBuildkit has the builder push straight from the build (`--push`).
	// One hop, and the fastest when it works.
	RouteBuildkit Route = iota

	// RouteCLI loads the image into the local store and pushes it with the
	// docker CLI, using credentials the CLI already holds.
	//
	// This exists because of a failure that reads like a network fault: the
	// builder has no registry credentials of its own and asks the client for
	// them over a session that lives only as long as the build command. An
	// emulated build is slow enough to outlive that session, so it reaches the
	// push stage to find nobody answering, and every attempt fails with
	// "no active session for <id>: context deadline exceeded" — after a build
	// that actually succeeded.
	RouteCLI
)

func (r Route) String() string {
	if r == RouteCLI {
		return "cli"
	}
	return "buildkit"
}

// ChooseRoute decides who pushes, from whether the builder is native to the
// platform being built.
//
// Slow means emulated, so that is what this follows: a builder running on the
// target's own architecture pushes from buildkit, an emulating one hands the
// image to the CLI. Multi-platform builds can only go via buildkit — nothing
// can load two architectures into one image store.
func ChooseRoute(ctx context.Context, bin, targetPlatform string) Route {
	if strings.Contains(targetPlatform, ",") {
		return RouteBuildkit
	}
	host := HostPlatform(ctx, bin)
	if host != targetPlatform {
		return RouteCLI
	}
	return RouteBuildkit
}

// Push uploads an image already in the local store.
func (r Runner) Push(ctx context.Context, image string) error {
	cmd := r.command(ctx, "push", image)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pushing %s: %w", image, err)
	}
	return nil
}

// command builds a docker invocation carrying the runner's environment.
func (r Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	bin := r.Docker
	if bin == "" {
		bin = DefaultDocker
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if r.ConfigDir != "" {
		// DOCKER_CONFIG rather than `docker login`: logging in rewrites the
		// user's ~/.docker/config.json and replaces whatever registry session
		// was already there. A private config directory holds the credential
		// for exactly this run and is removed after it.
		cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+r.ConfigDir)
	}
	return cmd
}

// WriteAuth creates a throwaway docker config directory holding one registry
// credential, and returns it with a function to remove it.
func WriteAuth(server, username, password string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "meimei-docker-")
	if err != nil {
		return "", func() {}, fmt.Errorf("creating a docker config directory: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }

	cfg := dockerConfig{Auths: map[string]dockerAuth{
		server: {Auth: base64.StdEncoding.EncodeToString([]byte(username + ":" + password))},
	}}
	body, err := json.Marshal(cfg)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("encoding docker credentials: %w", err)
	}
	// 0600: it holds a registry password for as long as the run lasts.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("writing docker credentials: %w", err)
	}
	return dir, cleanup, nil
}

type dockerConfig struct {
	Auths map[string]dockerAuth `json:"auths"`
}

type dockerAuth struct {
	Auth string `json:"auth"`
}
