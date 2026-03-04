package types

// AccountConfig holds AWS account configuration from .meimei.yaml.
type AccountConfig struct {
	Name              string `yaml:"name"`
	CodeDeployAppName string `yaml:"codedeploy_app_name"`
	CodeDeployBucket  string `yaml:"codedeploy_bucket"`
	BuildsTable       string `yaml:"builds_table"`
}

// DeploymentGroup represents a parsed CodeDeploy deployment group.
type DeploymentGroup struct {
	FullName string // e.g. "acme-dev-web1"
	Target   string // e.g. "acme-dev"
	Env      string // e.g. "dev"
	Cluster  string // e.g. "web1"
}

// Build represents a build record from DynamoDB.
type Build struct {
	BuildID string            `dynamodbav:"build_id"`
	AppName string            `dynamodbav:"app_name"`
	BuildBy string            `dynamodbav:"build_by"`
	BuildAt string            `dynamodbav:"build_at"`
	Release string            `dynamodbav:"release"`
	Bucket  string            `dynamodbav:"bucket"`
	Key     string            `dynamodbav:"key"`
	Extra   map[string]string `dynamodbav:"-"` // project-specific fields from config extra_columns
}

// DeploymentStatus tracks the status of a single deployment.
type DeploymentStatus struct {
	GroupName    string
	DeploymentID string
	Status       string // Created, InProgress, Succeeded, Failed, Stopped
}

// BuildFilter holds filtering options for build queries.
type BuildFilter struct {
	Limit      int
	FilterBy   string
	FilterName string
}
