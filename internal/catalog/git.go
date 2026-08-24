package catalog

import (
	"os/exec"
	"strings"
)

// Git is what the working tree says about the images a build would produce
// right now.
//
// Every field is best-effort. A project without git, or a fresh repo with no
// commits, is still a project whose images can be listed — it just cannot be
// told which commit a build would carry. So a failure here degrades the display
// rather than failing the command.
type Git struct {
	// SHA is the short commit hash, empty when unavailable.
	SHA string
	// FullSHA is the unabbreviated hash. It goes into the image's
	// org.opencontainers.image.revision label rather than the short form,
	// because a label is read long after the repo that made the abbreviation
	// unambiguous has moved on.
	FullSHA string
	// RemoteURL is origin's URL, for org.opencontainers.image.source. Empty for
	// a repo with no origin, which is not an error — it just means the image
	// cannot name where it came from.
	RemoteURL string
	// Dirty reports uncommitted changes. An image built from a dirty tree
	// cannot be traced back to a commit, which is worth saying out loud before
	// the build rather than after the push.
	Dirty bool
	// Available is false when git could not be consulted at all.
	Available bool
}

// ReadGit inspects the working tree at dir.
func ReadGit(dir string) Git {
	sha, err := gitOutput(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return Git{}
	}
	g := Git{SHA: sha, Available: true}

	// Everything past this point is decoration on a commit we already have, so
	// each failure costs only its own field.
	g.FullSHA, _ = gitOutput(dir, "rev-parse", "HEAD")
	g.RemoteURL, _ = gitOutput(dir, "remote", "get-url", "origin")

	// --porcelain is empty exactly when the tree is clean.
	status, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return g
	}
	g.Dirty = status != ""
	return g
}

// Tag is the image tag a build would produce for the given label.
//
// A label (a release id or a ticket) is used verbatim; otherwise the build is
// tagged by commit. The "sha-" prefix is reserved for commit tags so that ECR's
// keep-last-N-sha lifecycle rule can never sweep a release — which is also why
// a label starting with "sha-" is rejected at build time.
func (g Git) Tag(label string) string {
	if label != "" {
		return label
	}
	if g.SHA == "" {
		return ""
	}
	return "sha-" + g.SHA
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
