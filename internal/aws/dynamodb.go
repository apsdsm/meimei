package aws

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

func (c *Client) QueryBuilds(ctx context.Context, table, appName string, filter types.BuildFilter) ([]types.Build, error) {
	indexName := c.Config.IndexName

	input := &dynamodb.QueryInput{
		TableName:              &table,
		IndexName:              &indexName,
		KeyConditionExpression: aws.String("app_name = :t"),
		ScanIndexForward:       aws.Bool(false),
	}

	exprValues := map[string]ddbtypes.AttributeValue{
		":t": &ddbtypes.AttributeValueMemberS{Value: appName},
	}

	var filterParts []string
	exprNames := map[string]string{}

	if filter.FilterBy != "" {
		exprValues[":by"] = &ddbtypes.AttributeValueMemberS{Value: filter.FilterBy}
		filterParts = append(filterParts, "build_by = :by")
	}

	if filter.FilterName != "" {
		exprValues[":rel"] = &ddbtypes.AttributeValueMemberS{Value: filter.FilterName}
		exprNames["#rel"] = "release"
		filterParts = append(filterParts, "contains(#rel, :rel)")
	}

	if len(filterParts) > 0 {
		expr := filterParts[0]
		for _, p := range filterParts[1:] {
			expr += " AND " + p
		}
		input.FilterExpression = &expr
	}

	if len(exprNames) > 0 {
		input.ExpressionAttributeNames = exprNames
	}

	input.ExpressionAttributeValues = exprValues

	// When filtering, DynamoDB applies the filter after retrieving items but before
	// returning, so we fetch more and trim client-side.
	if len(filterParts) > 0 {
		input.Limit = aws.Int32(200)
	} else {
		input.Limit = aws.Int32(int32(filter.Limit))
	}

	resp, err := c.ddb.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("querying builds: %w", err)
	}

	// Unmarshal core fields
	var builds []types.Build
	if err := attributevalue.UnmarshalListOfMaps(resp.Items, &builds); err != nil {
		return nil, fmt.Errorf("unmarshalling builds: %w", err)
	}

	// Extract extra fields configured in extra_columns
	extraFields := extraFieldNames(c.Config.ExtraColumns)
	if len(extraFields) > 0 {
		for i, item := range resp.Items {
			builds[i].Extra = extractExtraFields(item, extraFields)
		}
	}

	// Sort by build_at descending (matching the bash `sort -k2 -r`)
	sort.Slice(builds, func(i, j int) bool {
		return builds[i].BuildAt > builds[j].BuildAt
	})

	// Trim to limit
	if len(builds) > filter.Limit {
		builds = builds[:filter.Limit]
	}

	return builds, nil
}

// extraFieldNames returns the DynamoDB attribute names from the extra columns config.
func extraFieldNames(columns []config.ExtraColumn) []string {
	names := make([]string, len(columns))
	for i, col := range columns {
		names[i] = col.Field
	}
	return names
}

// extractExtraFields pulls the named attributes from a DynamoDB item into a string map.
func extractExtraFields(item map[string]ddbtypes.AttributeValue, fields []string) map[string]string {
	extra := make(map[string]string)
	for _, field := range fields {
		if av, ok := item[field]; ok {
			if sv, ok := av.(*ddbtypes.AttributeValueMemberS); ok {
				extra[field] = sv.Value
			}
		}
	}
	return extra
}
