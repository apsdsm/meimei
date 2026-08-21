// Package registry talks to ECR: does a tag already exist, and how does docker
// authenticate to push it.
package registry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

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

// Exists reports whether a tag is already in a repository.
//
// This is what makes a re-run idempotent instead of an error: repositories are
// created with immutable tags, so pushing over an existing tag is refused by
// the registry. A build that has already been pushed is finished, not failed.
func (e *ECR) Exists(ctx context.Context, repo, tag string) (bool, error) {
	_, err := e.client.DescribeImages(ctx, &ecr.DescribeImagesInput{
		RepositoryName: aws.String(repo),
		ImageIds:       []ecrtypes.ImageIdentifier{{ImageTag: aws.String(tag)}},
	})
	if err == nil {
		return true, nil
	}

	// "no such tag" and "no such repository" are both answers rather than
	// faults — the first means go ahead, the second means the repository has
	// not been created yet, which is Terraform's job and worth saying plainly.
	var notFound *ecrtypes.ImageNotFoundException
	if errors.As(err, &notFound) {
		return false, nil
	}
	var noRepo *ecrtypes.RepositoryNotFoundException
	if errors.As(err, &noRepo) {
		return false, fmt.Errorf("repository %q does not exist in account %s (%s) — "+
			"it is created by Terraform, not by meimei", repo, e.account, e.region)
	}
	return false, fmt.Errorf("checking %s:%s: %w", repo, tag, err)
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
