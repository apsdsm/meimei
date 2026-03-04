package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
	cdtypes "github.com/aws/aws-sdk-go-v2/service/codedeploy/types"
	"github.com/apsdsm/meimei/internal/types"
)

// ParseDeploymentGroup splits a CodeDeploy group name like "acme-dev-web1" into
// its target ("acme-dev"), env ("dev"), and cluster ("web1") parts.
// The cluster is everything after the last hyphen; the env is the last segment
// of the remaining target prefix.
func ParseDeploymentGroup(name string) types.DeploymentGroup {
	lastDash := strings.LastIndex(name, "-")
	if lastDash < 0 {
		return types.DeploymentGroup{FullName: name, Target: name, Env: name, Cluster: name}
	}

	cluster := name[lastDash+1:]
	target := name[:lastDash]
	env := target
	if idx := strings.LastIndex(target, "-"); idx >= 0 {
		env = target[idx+1:]
	}

	return types.DeploymentGroup{
		FullName: name,
		Target:   target,
		Env:      env,
		Cluster:  cluster,
	}
}

func (c *Client) ListDeploymentGroups(ctx context.Context, appName string) ([]types.DeploymentGroup, error) {
	resp, err := c.cd.ListDeploymentGroups(ctx, &codedeploy.ListDeploymentGroupsInput{
		ApplicationName: &appName,
	})
	if err != nil {
		return nil, fmt.Errorf("listing deployment groups: %w", err)
	}

	groups := make([]types.DeploymentGroup, 0, len(resp.DeploymentGroups))
	for _, name := range resp.DeploymentGroups {
		groups = append(groups, ParseDeploymentGroup(name))
	}
	return groups, nil
}

func (c *Client) CreateDeployment(ctx context.Context, appName, group, bucket, key, buildID, user string) (string, error) {
	description := fmt.Sprintf("Manual deploy of %s by %s", buildID, user)
	configName := "CodeDeployDefault.AllAtOnce"
	bundleType := cdtypes.BundleTypeZip
	fileExists := cdtypes.FileExistsBehaviorOverwrite

	resp, err := c.cd.CreateDeployment(ctx, &codedeploy.CreateDeploymentInput{
		ApplicationName:      &appName,
		DeploymentGroupName:  &group,
		DeploymentConfigName: &configName,
		Description:          &description,
		FileExistsBehavior:   fileExists,
		Revision: &cdtypes.RevisionLocation{
			RevisionType: cdtypes.RevisionLocationTypeS3,
			S3Location: &cdtypes.S3Location{
				Bucket:     &bucket,
				Key:        &key,
				BundleType: bundleType,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating deployment for %s: %w", group, err)
	}

	return *resp.DeploymentId, nil
}

func (c *Client) GetDeploymentStatus(ctx context.Context, deploymentID string) (string, error) {
	resp, err := c.cd.GetDeployment(ctx, &codedeploy.GetDeploymentInput{
		DeploymentId: &deploymentID,
	})
	if err != nil {
		return "", fmt.Errorf("getting deployment %s: %w", deploymentID, err)
	}

	return string(resp.DeploymentInfo.Status), nil
}
