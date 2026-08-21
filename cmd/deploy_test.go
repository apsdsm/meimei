package cmd

import (
	"strings"
	"testing"

	"github.com/apsdsm/meimei/internal/registry"
)

// The refusal message is the feature: somebody who forgot to push has to be told
// what is missing, where it was looked for, and what to run. So it is asserted
// rather than eyeballed.

const where = "111122223333, ap-northeast-1"

func noSuggestion(string) string { return "" }

func finding(service, tag string, status registry.Status) registry.Finding {
	return registry.Finding{
		Ref:    registry.Ref{Service: service, Repo: "acme-" + service, Tag: tag},
		Status: status,
	}
}

func TestReviewAllowsADeployWhereEveryImageIsThere(t *testing.T) {
	_, err := reviewFindings([]registry.Finding{
		finding("api", "rel-9f2c1a7b0e44", registry.Present),
		finding("sysadmin-web-spa", "rel-9f2c1a7b0e44", registry.Present),
	}, where, noSuggestion)

	if err != nil {
		t.Errorf("err = %v, want a deploy that goes ahead", err)
	}
}

// Every missing image in one go. Reporting the first would make an operator
// discover the rest one deploy at a time, which is the failure this prevents.
func TestReviewReportsEveryMissingImageAtOnce(t *testing.T) {
	_, err := reviewFindings([]registry.Finding{
		finding("api", "rel-9f2c1a7b0e44", registry.Missing),
		finding("sysadmin-web-spa", "rel-9f2c1a7b0e44", registry.Present),
		finding("user-web-spa", "rel-9f2c1a7b0e44", registry.Missing),
	}, where, noSuggestion)

	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	got := err.Error()
	for _, want := range []string{"acme-api", "acme-user-web-spa", "2 images are not in ECR", where} {
		if !strings.Contains(got, want) {
			t.Errorf("message is missing %q:\n%s", want, got)
		}
	}
	// The one that IS there must not be listed as a problem.
	if strings.Contains(got, "acme-sysadmin-web-spa") {
		t.Errorf("message names an image that is present:\n%s", got)
	}
	// And it has to name the fix, with the services the operator typed rather
	// than the repositories they map to.
	if !strings.Contains(got, "meimei build api user-web-spa --push") {
		t.Errorf("message does not name the command to run:\n%s", got)
	}
	if !strings.Contains(got, "nothing was changed") {
		t.Errorf("message does not say the cluster is untouched:\n%s", got)
	}
}

func TestReviewSuggestsATagThatExists(t *testing.T) {
	_, err := reviewFindings(
		[]registry.Finding{finding("api", "rel-doesnotexist", registry.Missing)},
		where,
		func(string) string { return "rel-4a1b2c3d5e6f      (pushed 2026-08-20 05:37)" },
	)

	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "rel-4a1b2c3d5e6f") {
		t.Errorf("message does not offer the alternative:\n%s", err)
	}
}

// An ambiguously-tagged image is refused for a different reason from a missing
// one, and the message has to carry that reason — otherwise it reads as "your
// image is broken" when the image is fine and its NAME is the problem.
func TestReviewRefusesAnAmbiguouslyTaggedImage(t *testing.T) {
	f := finding("api", "rel-9f2c1a7b0e44", registry.Ambiguous)
	f.Aliases = []string{"bootstrap"}

	_, err := reviewFindings([]registry.Finding{f}, where, noSuggestion)

	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	got := err.Error()
	if !strings.Contains(got, "bootstrap") {
		t.Errorf("message does not name the other tag:\n%s", got)
	}
	if !strings.Contains(got, "--skip-image-check") {
		t.Errorf("message does not name the escape hatch:\n%s", got)
	}
	if strings.Contains(got, "not in ECR") {
		t.Errorf("message describes it as missing, which it is not:\n%s", got)
	}
}

// A registry that could not be asked is a warning, never a refusal: a missing
// read permission must not be able to block a deploy that would work.
func TestReviewWarnsButAllowsWhenTheRegistryCouldNotBeAsked(t *testing.T) {
	f := finding("api", "rel-9f2c1a7b0e44", registry.Unknown)
	f.Why = "AccessDeniedException"

	warnings, err := reviewFindings([]registry.Finding{f}, where, noSuggestion)

	if err != nil {
		t.Errorf("err = %v, want the deploy to go ahead", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "AccessDenied") {
		t.Errorf("warnings = %v, want one naming what the registry said", warnings)
	}
}
