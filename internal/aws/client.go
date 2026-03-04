package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

type Client struct {
	cfg       aws.Config
	AccountID string
	Account   *types.AccountConfig
	Config    *config.ProjectConfig
	cd        *codedeploy.Client
	ddb       *dynamodb.Client
}

func NewClient(ctx context.Context, projectCfg *config.ProjectConfig) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("getting caller identity: %w", err)
	}

	accountID := *identity.Account

	acct, err := projectCfg.ResolveAccount(accountID)
	if err != nil {
		return nil, err
	}

	return &Client{
		cfg:       cfg,
		AccountID: accountID,
		Account:   acct,
		Config:    projectCfg,
		cd:        codedeploy.NewFromConfig(cfg),
		ddb:       dynamodb.NewFromConfig(cfg),
	}, nil
}
