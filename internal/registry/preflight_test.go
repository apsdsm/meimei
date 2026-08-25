package registry

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func ref() Ref {
	return Ref{Name: "api", Repo: "jjc2-api", Tag: "rel-9f2c1a7b0e44"}
}

func TestClassifyPresentWhenTheTagIsTheOnlyOne(t *testing.T) {
	pushed := time.Date(2026, 8, 20, 5, 37, 0, 0, time.UTC)
	img := &Image{Digest: "sha256:abc", PushedAt: pushed, Tags: []string{"rel-9f2c1a7b0e44"}}

	f := classify(ref(), img, nil)

	if f.Status != Present {
		t.Errorf("status = %v, want Present", f.Status)
	}
	if len(f.Aliases) != 0 {
		t.Errorf("aliases = %v, want none", f.Aliases)
	}
	if !f.PushedAt.Equal(pushed) {
		t.Errorf("pushedAt = %v, want %v", f.PushedAt, pushed)
	}
}

func TestClassifyMissingWhenThereIsNoImage(t *testing.T) {
	f := classify(ref(), nil, nil)

	if f.Status != Missing {
		t.Errorf("status = %v, want Missing", f.Status)
	}
}

// The case this whole check exists for. A digest carrying a second tag means the
// image was built as one of them and is being promoted as possibly the other,
// and the tag it was built as is what the running code reports about itself.
func TestClassifyAmbiguousWhenTheImageCarriesAnotherTag(t *testing.T) {
	img := &Image{Tags: []string{"rel-9f2c1a7b0e44", "bootstrap"}}

	f := classify(ref(), img, nil)

	if f.Status != Ambiguous {
		t.Errorf("status = %v, want Ambiguous", f.Status)
	}
	if got := strings.Join(f.Aliases, ","); got != "bootstrap" {
		t.Errorf("aliases = %q, want the OTHER tag only", got)
	}
}

// A read permission that is absent, a denied call, a network fault: none of them
// are evidence that an image is missing, and treating them as such would let a
// registry we cannot see block a deploy that would otherwise work.
func TestClassifyUnknownIsNeverMissing(t *testing.T) {
	f := classify(ref(), nil, errors.New("AccessDeniedException: not authorized to perform ecr:DescribeImages"))

	if f.Status == Missing {
		t.Fatalf("status = Missing, want Unknown — a failed read is not an absent image")
	}
	if f.Status != Unknown {
		t.Errorf("status = %v, want Unknown", f.Status)
	}
	if !strings.Contains(f.Why, "AccessDenied") {
		t.Errorf("why = %q, want it to carry what the registry said", f.Why)
	}
}

func TestOtherTagsExcludesTheOneAskedAboutAndSortsTheRest(t *testing.T) {
	img := Image{Tags: []string{"rel-9f2c1a7b0e44", "latest", "bootstrap"}}

	if got := strings.Join(img.OtherTags("rel-9f2c1a7b0e44"), ","); got != "bootstrap,latest" {
		t.Errorf("otherTags = %q, want the rest in a stable order", got)
	}
	// Asked about a tag the image does not carry, every tag is another tag —
	// which is the honest answer rather than an empty one.
	if got := len(img.OtherTags("something-else")); got != 3 {
		t.Errorf("otherTags for an unknown tag = %d, want all 3", got)
	}
}
