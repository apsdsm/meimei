// Package awsx builds AWS clients for a named account and proves they point at
// it.
//
// meimei talks to two accounts in one run — the registry's and the target's —
// so a single ambient credential set is not enough. Each caller says which
// account and profile it wants, gets its own config, and the account is
// checked before anything is done with it.
package awsx

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Session is an authenticated AWS config, already checked against the account
// it was asked for.
type Session struct {
	Config  aws.Config
	Account string
	Profile string
	Region  string
}

// Open loads credentials and verifies they are for wantAccount.
//
// The profile is used when named and configured, so nobody has to switch
// AWS_PROFILE by hand; when it is absent the ambient credentials are used
// instead, which is what CI has through OIDC. Either way the account is
// verified, because "which credentials am I using" is the question that
// silently ruins a deploy.
func Open(ctx context.Context, profile, region, wantAccount string) (*Session, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		// A named profile that does not exist locally is normal in CI, so fall
		// back to ambient credentials rather than failing here. The account
		// check below is what decides whether they are the right ones.
		if profile == "" {
			return nil, fmt.Errorf("loading AWS config: %w", err)
		}
		cfg, err = awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return nil, fmt.Errorf("loading AWS config: %w", err)
		}
		profile = ""
	}

	got, err := callerAccount(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w\n       fix: aws sso login%s", err, profileHint(profile))
	}
	if wantAccount != "" && got != wantAccount {
		return nil, fmt.Errorf("wrong AWS account: need %s, active credentials are for %s"+
			"\n       fix: aws sso login%s", wantAccount, got, profileHint(profile))
	}

	return &Session{Config: cfg, Account: got, Profile: profile, Region: cfg.Region}, nil
}

func callerAccount(ctx context.Context, cfg aws.Config) (string, error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("no usable AWS credentials: %w", err)
	}
	return aws.ToString(out.Account), nil
}

func profileHint(profile string) string {
	if profile == "" {
		return ""
	}
	return " --profile " + profile
}
