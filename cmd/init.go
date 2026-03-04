package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codedeploy"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize meimei configuration for a project",
	Long:  "Validates existing AWS infrastructure and generates a .meimei.yaml config file in the current directory.",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().String("project", "", "Project name (used in .meimei.yaml)")
	initCmd.Flags().String("app", "", "CodeDeploy application name")
	initCmd.Flags().String("bucket", "", "S3 bucket for build artifacts")
	initCmd.Flags().String("table", "", "DynamoDB builds table name")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Load AWS config and get account ID
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("getting caller identity (are you authenticated?): %w", err)
	}

	accountID := *identity.Account
	fmt.Printf("AWS Account: %s\n\n", accountID)

	projectName, _ := cmd.Flags().GetString("project")
	appName, _ := cmd.Flags().GetString("app")
	bucketName, _ := cmd.Flags().GetString("bucket")
	tableName, _ := cmd.Flags().GetString("table")

	// Validate infrastructure
	allValid := true

	// Check CodeDeploy application
	if appName != "" {
		cdClient := codedeploy.NewFromConfig(cfg)
		_, err := cdClient.ListDeploymentGroups(ctx, &codedeploy.ListDeploymentGroupsInput{
			ApplicationName: &appName,
		})
		if err != nil {
			fmt.Printf("✗ CodeDeploy application '%s' not accessible\n", appName)
			fmt.Printf("  Error: %v\n\n", err)
			allValid = false
		} else {
			fmt.Printf("✓ CodeDeploy application '%s' found\n", appName)
		}
	}

	// Check DynamoDB table
	if tableName != "" {
		ddbClient := dynamodb.NewFromConfig(cfg)
		resp, err := ddbClient.DescribeTable(ctx, &dynamodb.DescribeTableInput{
			TableName: &tableName,
		})
		if err != nil {
			fmt.Printf("✗ DynamoDB table '%s' not found\n", tableName)
			fmt.Printf("\n  Create it with:\n\n")
			fmt.Printf("    aws dynamodb create-table \\\n")
			fmt.Printf("      --table-name %s \\\n", tableName)
			fmt.Printf("      --attribute-definitions \\\n")
			fmt.Printf("        AttributeName=build_id,AttributeType=S \\\n")
			fmt.Printf("        AttributeName=app_name,AttributeType=S \\\n")
			fmt.Printf("        AttributeName=build_at,AttributeType=S \\\n")
			fmt.Printf("      --key-schema AttributeName=build_id,KeyType=HASH \\\n")
			fmt.Printf("      --global-secondary-indexes '[{\n")
			fmt.Printf("        \"IndexName\": \"app_name-index\",\n")
			fmt.Printf("        \"KeySchema\": [\n")
			fmt.Printf("          {\"AttributeName\": \"app_name\", \"KeyType\": \"HASH\"},\n")
			fmt.Printf("          {\"AttributeName\": \"build_at\", \"KeyType\": \"RANGE\"}\n")
			fmt.Printf("        ],\n")
			fmt.Printf("        \"Projection\": {\"ProjectionType\": \"ALL\"}\n")
			fmt.Printf("      }]' \\\n")
			fmt.Printf("      --billing-mode PAY_PER_REQUEST\n\n")
			allValid = false
		} else {
			fmt.Printf("✓ DynamoDB table '%s' found (%s)\n", tableName, string(resp.Table.TableStatus))

			// Check for GSI
			hasIndex := false
			for _, gsi := range resp.Table.GlobalSecondaryIndexes {
				if aws.ToString(gsi.IndexName) == "app_name-index" {
					hasIndex = true
					break
				}
			}
			if !hasIndex {
				fmt.Printf("  ⚠ GSI 'app_name-index' not found on table\n")
				allValid = false
			} else {
				fmt.Printf("  ✓ GSI 'app_name-index' present\n")
			}
		}
	}

	fmt.Println()

	// Generate .meimei.yaml
	acctCfg := types.AccountConfig{
		CodeDeployAppName: appName,
		CodeDeployBucket:  bucketName,
		BuildsTable:       tableName,
	}

	projCfg := config.ProjectConfig{
		Project: projectName,
		Accounts: map[string]types.AccountConfig{
			accountID: acctCfg,
		},
	}

	data, err := yaml.Marshal(&projCfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	outPath := filepath.Join(".", ".meimei.yaml")

	if _, err := os.Stat(outPath); err == nil {
		fmt.Printf("⚠ %s already exists — not overwriting\n", outPath)
		if !allValid {
			fmt.Println("Some infrastructure checks failed. See above for details.")
		}
		return nil
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	fmt.Printf("✓ Wrote %s\n", outPath)
	if !allValid {
		fmt.Println("\nSome infrastructure checks failed. See above for details.")
	} else {
		fmt.Println("\nAll checks passed. You're ready to use meimei!")
	}

	return nil
}
