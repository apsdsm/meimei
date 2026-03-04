# meimei

A CLI for browsing builds and deploying via AWS CodeDeploy. Features an interactive TUI for selecting deployment targets, picking builds, and monitoring deployment progress.

<p align="center">
  <img src="meimei.jpeg" alt="meimei" width="400">
</p>

## Install

```bash
go install github.com/apsdsm/meimei@latest
```

Requires Go 1.25.5+.

## Quick start

1. Add a `.meimei.yaml` to your project root (see [Configuration](#configuration))
2. Authenticate with AWS (`aws sso login`, environment variables, etc.)
3. Browse builds:
   ```bash
   meimei builds
   ```
4. Deploy:
   ```bash
   meimei deploy
   ```

## Commands

### `meimei builds`

Opens an interactive table of available builds.

| Flag       | Description                              | Default |
|------------|------------------------------------------|---------|
| `--limit`  | Maximum number of builds to show         | 20      |
| `--mine`   | Only show builds by current `$USER`      |         |
| `--by`     | Only show builds by a specific user      |         |
| `--filter` | Only show builds whose name contains this string |  |

Keyboard: `/` filter, `j/k` or `↑/↓` scroll, `esc` clear filter, `q` quit.

### `meimei deploy`

Interactive deployment flow: select targets, pick a build, confirm, deploy, and poll status until completion. Accepts the same flags as `builds`.

The flow:

1. **Select targets** — deployment groups shown in columns by environment. `space` toggles, `a` selects all in column, `n` deselects all, `h/l` or `←/→` switches columns, `enter` confirms.
2. **Select build** — same table as `builds`. `enter` selects.
3. **Confirm** — review targets, build, and S3 location. Type `yes` to proceed.
4. **Deploy** — creates CodeDeploy deployments and polls every 5 seconds until all reach a terminal state (Succeeded, Failed, or Stopped).

### `meimei init`

Scaffolds a `.meimei.yaml` in the current directory. Validates that the referenced AWS infrastructure exists.

| Flag        | Description                    |
|-------------|--------------------------------|
| `--project` | Project name                   |
| `--app`     | CodeDeploy application name    |
| `--bucket`  | S3 bucket for build artifacts  |
| `--table`   | DynamoDB builds table name     |

### Global flags

| Flag       | Description                                          |
|------------|------------------------------------------------------|
| `--config` | Path to `.meimei.yaml` (overrides directory-tree walk) |

## Configuration

Meimei looks for `.meimei.yaml` by walking up from the current directory to the filesystem root. Use `--config` to specify an explicit path.

The active AWS account ID (via STS) determines which account entry is used.

```yaml
# Project name — used as a label
project: my-app

# One entry per AWS account
accounts:
  "123456789012":
    name: production                  # optional friendly name
    codedeploy_app_name: my-app       # CodeDeploy application name
    codedeploy_bucket: my-app-builds  # S3 bucket with build artifacts
    builds_table: my-app-builds       # DynamoDB table
  "987654321098":
    name: staging
    codedeploy_app_name: my-app-stg
    codedeploy_bucket: my-app-builds-stg
    builds_table: my-app-builds

# Optional: extra DynamoDB attributes to show in the builds table
extra_columns:
  - field: git_commit    # DynamoDB attribute name
    header: Commit       # column header
  - field: version
    header: Version

# Optional: GSI name (default: "app_name-index")
index_name: app_name-index
```

## AWS infrastructure contract

Meimei does not provision infrastructure. The following resources must exist before use.

### DynamoDB table

Stores the build catalog. Each item represents a build artifact.

**Required attributes:**

| Attribute  | Type   | Description                    |
|------------|--------|--------------------------------|
| `build_id` | S (PK) | Unique build identifier       |
| `app_name` | S      | CodeDeploy application name    |
| `build_at` | S      | Build timestamp (ISO 8601)     |
| `build_by` | S      | Username of the builder        |
| `release`  | S      | Release name                   |
| `bucket`   | S      | S3 bucket containing artifact  |
| `key`      | S      | S3 object key                  |

**Required GSI** (`app_name-index` by default):

- Partition key: `app_name` (S)
- Sort key: `build_at` (S)
- Projection: ALL

```bash
aws dynamodb create-table \
  --table-name my-app-builds \
  --attribute-definitions \
    AttributeName=build_id,AttributeType=S \
    AttributeName=app_name,AttributeType=S \
    AttributeName=build_at,AttributeType=S \
  --key-schema AttributeName=build_id,KeyType=HASH \
  --global-secondary-indexes '[{
    "IndexName": "app_name-index",
    "KeySchema": [
      {"AttributeName": "app_name", "KeyType": "HASH"},
      {"AttributeName": "build_at", "KeyType": "RANGE"}
    ],
    "Projection": {"ProjectionType": "ALL"}
  }]' \
  --billing-mode PAY_PER_REQUEST
```

Add any fields referenced by `extra_columns` to your build items — no schema change needed since DynamoDB is schemaless.

### CodeDeploy application

A CodeDeploy application with one or more deployment groups. Meimei lists groups automatically and presents them for selection during deploy.

### S3 bucket

Stores build artifacts (ZIP bundles). Referenced by the `bucket` and `key` fields in each DynamoDB build item.

### IAM permissions

The calling principal needs:

```
sts:GetCallerIdentity
codedeploy:ListDeploymentGroups
codedeploy:CreateDeployment
codedeploy:GetDeployment
dynamodb:Query          (table + GSI)
dynamodb:DescribeTable  (used by init)
s3:GetObject            (CodeDeploy fetches the artifact)
```

## Deployment group naming

Meimei parses deployment group names as `{target}-{cluster}`, splitting on the last hyphen:

```
acme-dev-foo
  target:  acme-dev
  env:     dev       (last segment of target)
  cluster: foo

acme-stg-qa1
  target:  acme-stg
  env:     atg
  cluster: qa1
```

The `env` value determines column ordering in the target selection screen: `dev`, `stg`, `prd`, then alphabetical.
