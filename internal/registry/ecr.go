// Package registry talks to ECR: does a tag already exist, and how does docker
// authenticate to push it.
package registry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"

	"github.com/apsdsm/meimei/internal/awsx"
)

// ECR is one account's registry in one region.
type ECR struct {
	client  *ecr.Client
	account string
	region  string
}

// New builds a registry client from an already-verified session.
func New(s *awsx.Session) *ECR {
	return &ECR{client: ecr.NewFromConfig(s.Config), account: s.Account, region: s.Config.Region}
}

// Host is the registry hostname docker pushes to.
func (e *ECR) Host() string {
	return e.account + ".dkr.ecr." + e.region + ".amazonaws.com"
}

// Image is what the registry holds under one tag.
type Image struct {
	Digest   string
	PushedAt time.Time

	// Tags is EVERY tag on this image, not just the one that was asked about —
	// which is how a caller can tell a tag that uniquely names a build from one
	// of several names for the same bytes.
	Tags []string
}

// OtherTags are this image's tags apart from the one named, in a stable order.
func (i Image) OtherTags(tag string) []string {
	var out []string
	for _, t := range i.Tags {
		if t != tag {
			out = append(out, t)
		}
	}
	sort.Strings(out)

	return out
}

// Describe reports what the registry holds under a tag, or nil when there is
// nothing there.
//
// A missing tag is an answer, not a fault: it is what makes a re-push idempotent
// (repositories here have immutable tags, so pushing over an existing one is
// refused) and it is what a deploy preflight is asking about.
func (e *ECR) Describe(ctx context.Context, repo, tag string) (*Image, error) {
	out, err := e.client.DescribeImages(ctx, &ecr.DescribeImagesInput{
		RepositoryName: aws.String(repo),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String(tag)}},
	})
	if err == nil {
		if len(out.ImageDetails) == 0 {
			return nil, nil
		}
		img := imageFrom(out.ImageDetails[0])

		return &img, nil
	}

	var notFound *ecrtypes.ImageNotFoundException
	if errors.As(err, &notFound) {
		return nil, nil
	}
	// A repository that does not exist is a different kind of missing, and
	// saying so saves somebody looking for a build that could never have been
	// pushed: the repository is Terraform's to create, not meimei's.
	var noRepo *ecrtypes.RepositoryNotFoundException
	if errors.As(err, &noRepo) {
		return nil, fmt.Errorf("repository %q does not exist in account %s (%s) — "+
			"it is created by Terraform, not by meimei", repo, e.account, e.region)
	}

	return nil, fmt.Errorf("checking %s:%s: %w", repo, tag, err)
}

func imageFrom(d ecrtypes.ImageDetail) Image {
	return Image{
		Digest:   aws.ToString(d.ImageDigest),
		PushedAt: aws.ToTime(d.ImagePushedAt),
		Tags:     d.ImageTags,
	}
}

// Exists reports whether a tag is already in a repository.
//
// This is what makes a re-run idempotent instead of an error: a build that has
// already been pushed is finished, not failed.
func (e *ECR) Exists(ctx context.Context, repo, tag string) (bool, error) {
	img, err := e.Describe(ctx, repo, tag)
	if err != nil {
		return false, err
	}

	return img != nil, nil
}

// Auth is a docker registry credential.
type Auth struct {
	Server   string
	Username string
	Password string
}

// Login fetches a docker credential for this registry.
//
// The token is short-lived and scoped to the caller's IAM permissions, so it is
// fetched per run rather than stored.
func (e *ECR) Login(ctx context.Context) (Auth, error) {
	out, err := e.client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return Auth{}, fmt.Errorf("getting an ECR token: %w", err)
	}
	if len(out.AuthorizationData) == 0 {
		return Auth{}, fmt.Errorf("ECR returned no authorization data")
	}

	raw, err := base64.StdEncoding.DecodeString(aws.ToString(out.AuthorizationData[0].AuthorizationToken))
	if err != nil {
		return Auth{}, fmt.Errorf("decoding the ECR token: %w", err)
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return Auth{}, fmt.Errorf("malformed ECR token")
	}
	return Auth{Server: e.Host(), Username: user, Password: pass}, nil
}
