package registry

import (
	"context"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// Asking the registry about the images a deploy is about to promote, before it
// promotes any of them.
//
// Two questions answered by one DescribeImages call, because both answers are in
// the same response: is the tag there, and is it the only tag on that image.

// Ref names one image a deploy intends to promote.
type Ref struct {
	// Service is what the operator typed, kept so a failure can name the thing
	// they know rather than the repository it maps to.
	Service string
	Repo    string
	Tag     string
}

// Status is what the registry could be told about a Ref.
type Status int

const (
	// Present: the tag is there, and it is the only tag on its image.
	Present Status = iota

	// Missing: the registry answered, and the tag is not there.
	Missing

	// Unknown: the registry could not be asked — no credentials for its
	// account, a denied call, a network failure.
	//
	// Never folded into Missing. A deploy that would otherwise work must not
	// become impossible because a read permission is absent, so an unanswered
	// question is reported and the deploy proceeds.
	Unknown

	// Ambiguous: the tag is there, and its image carries other tags too.
	//
	// meimei pushes exactly one tag per build, so a second tag on the same
	// image was put there by something else — a promote by hand, or Terraform's
	// placeholder pushed over. That matters beyond tidiness: the image was
	// built as ONE of those tags, that is the tag baked into it as
	// MEIMEI_BUILD_ID, and it is what the running code reports about itself. A
	// task definition naming a different one describes the same bytes by a name
	// the code inside does not recognise.
	Ambiguous
)

// Finding is what a preflight learned about one Ref.
type Finding struct {
	Ref

	Status Status

	// Why carries what the registry said, for a Missing or an Unknown that has
	// more to it than the status.
	Why string

	// Aliases are the OTHER tags on this image, set when Status is Ambiguous.
	Aliases []string

	// PushedAt is when the image arrived, set when it is there at all.
	PushedAt time.Time
}

// Preflight asks about every ref, and answers about every ref.
//
// It returns findings rather than an error because "some of these are missing"
// is the useful answer and "the first one is missing" is not: a deploy can name
// several images — --all, or one revision carrying several containers — and an
// operator discovering them one deploy at a time is the failure this exists to
// stop.
func (e *ECR) Preflight(ctx context.Context, refs []Ref) []Finding {
	out := make([]Finding, 0, len(refs))

	for _, r := range refs {
		img, err := e.Describe(ctx, r.Repo, r.Tag)
		out = append(out, classify(r, img, err))
	}

	return out
}

// classify turns one registry answer into a finding.
//
// Separate from the call that produced it, and pure, because the rules are the
// part worth pinning down in a test: which answers are refusals, and — the one
// that matters — which are not.
func classify(r Ref, img *Image, err error) Finding {
	f := Finding{Ref: r}

	switch {
	case err != nil:
		f.Status = Unknown
		f.Why = err.Error()
	case img == nil:
		f.Status = Missing
	default:
		f.PushedAt = img.PushedAt
		f.Aliases = img.OtherTags(r.Tag)
		if len(f.Aliases) > 0 {
			f.Status = Ambiguous
		} else {
			f.Status = Present
		}
	}

	return f
}

// Recent lists a repository's tags, most recently pushed first.
//
// For suggesting a tag that does exist after asking for one that does not, so a
// small number is enough and paging is not worth it — these repositories are
// kept to their last thirty images by a lifecycle rule.
func (e *ECR) Recent(ctx context.Context, repo string, n int) ([]Image, error) {
	out, err := e.client.DescribeImages(ctx, &ecr.DescribeImagesInput{
		RepositoryName: aws.String(repo),
		Filter:         &ecrtypes.DescribeImagesFilter{TagStatus: ecrtypes.TagStatusTagged},
		MaxResults:     aws.Int32(100),
	})
	if err != nil {
		return nil, err
	}

	images := make([]Image, 0, len(out.ImageDetails))
	for _, d := range out.ImageDetails {
		images = append(images, imageFrom(d))
	}

	// ECR does not order by push time, so sorting is ours to do.
	sort.Slice(images, func(i, j int) bool { return images[i].PushedAt.After(images[j].PushedAt) })

	if len(images) > n {
		images = images[:n]
	}

	return images, nil
}
