# `.meimei.toml` reference

What a correct config file looks like, key by key. This documents **config version 2**.

A binary reads one config version and refuses every other one by name. There is no backwards
compatibility and none is planned.

## A complete file

```toml
version = 2

[project]
name     = "jjc2"                # a label for this repo in output; names nothing in AWS
region   = "ap-northeast-1"
platform = "linux/arm64"         # default build platform

# One buildable container image per table. `name` is what you type at the CLI;
# `repository` and `container` are the two names that face AWS.
[[builds]]
name       = "api"
repository = "jjc2-api"          # exact ECR repository name
container  = "api"               # exact container definition name
short      = "aa"                # optional abbreviation, for a future keyboard shortcut
dockerfile = "services/api/jjc2_system_api/Dockerfile"
context    = "."                 # the default: the build root, not the Dockerfile's directory
group      = "api"               # presentational only
color      = "blue"
platform   = "linux/amd64"       # optional, overrides project.platform for this build
disabled   = false               # keeps a declaration out of every build

# Where images are pushed. One registry for every target: an image is built once
# and deployed anywhere.
[registry]
account = "036558358799"         # asserted against the live caller identity before any push
profile = "jjc2-shared"          # AWS SSO profile; empty means ambient credentials (CI)
# region falls through to project.region

# Somewhere images get deployed to: a cluster, and a scope inside it.
[[targets]]
name     = "dev1"                # the label you pass to --target
account  = "477187160695"
profile  = "jjc2-dev"
cluster  = "jjc2-dev1-ecs-cluster-dev1"
services = ["jjc2-dev1-internal", "jjc2-dev1-external"]   # optional; exact ECS service names
timeout  = "10m"                 # optional; how long to follow a rollout
# region falls through to project.region
```

The file is found by walking up from the working directory. The directory holding it is the **build
root**, and every path in the file is relative to that directory.

## Every key

### Top level

| Key | Required | Default | What it is |
|---|---|---|---|
| `version` | yes | — | config format version. Must be `2`, and must be the first line: TOML requires bare top-level keys before the first `[table]`. |
| `[project]` | yes | — | one table |
| `[[builds]]` | yes, at least one | — | one table per buildable image |
| `[registry]` | no | absent | required unless every build uses `--no-push`; `deploy` needs it too |
| `[[targets]]` | no | none | `deploy` fails at run time if none is declared |

### `[project]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `name` | yes | — | a label for this repository in meimei's output. It names nothing in AWS. |
| `region` | no | empty | the default for `registry.region` and every `target.region` |
| `platform` | no | `linux/arm64` | the default for every build's platform |

### `[[builds]]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `name` | yes | — | meimei's own label: what you type at the CLI and what appears in output. Names nothing in AWS. Unique across the file. |
| `repository` | yes | — | the exact ECR repository name. Used verbatim. |
| `container` | yes | — | the exact container definition name inside the ECS task definition. Matched verbatim. |
| `dockerfile` | yes | — | path relative to the build root. Not absolute, and may not escape the root. |
| `context` | no | `"."` | docker build context, relative to the build root. Same two restrictions. |
| `platform` | no | `project.platform` | the image's OCI platform |
| `short` | no | empty | abbreviation for a future keyboard shortcut. Unique across the file if set. |
| `group` | no | empty | presentational only. Arranges the list on screen and carries no deployment meaning. |
| `color` | no | empty | display colour |
| `disabled` | no | `false` | excluded from `--all` |

`repository` and `container` are **required rather than derived from `name`**, and are not defaulted
to it either. A default is a derivation with a friendlier face, and it would rule out any
infrastructure that does not happen to match the convention. meimei creates neither the repository
nor the container, so both always name something that already exists.

Two builds may share a `repository`. That is one image reaching two environments under different
container names. Two builds may not share a `name`.

### `[registry]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `account` | yes, if the table is present | — | AWS account id. Declared rather than discovered, and asserted against the live caller identity before any registry call — repositories here are immutable, so a push into the wrong account cannot be taken back. |
| `region` | yes, if the table is present | `project.region` | ECR region |
| `profile` | no | empty | AWS shared-config profile. Empty means ambient credentials. |

The registry host is `<account>.dkr.ecr.<region>.amazonaws.com`. That is AWS's URL format rather than
a naming convention, so it is composed rather than declared.

### `[[targets]]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `name` | yes | — | the label `--target` takes. Names nothing in AWS. Unique across the file. |
| `cluster` | yes | — | the exact ECS cluster name |
| `account` | yes | — | AWS account id, asserted before the deploy runs |
| `region` | yes | `project.region` | the cluster's region |
| `profile` | no | empty | AWS shared-config profile |
| `services` | no | the whole cluster | the exact ECS service names this target addresses |
| `timeout` | no | 10m | how long to follow a rollout, as a Go duration string. A string because TOML has no duration type. |

`--target` is optional when exactly one target is declared, and required as soon as there are two —
at which point the error names them all.

#### `services`

This is the one fact about a deploy that cannot be discovered. ECS has no notion of an environment,
so nothing on the cluster says which of its services are production and which are staging.

It is a **scope, not an instruction**. Naming three services does not make a deploy roll three
services; it makes those three the only ones a build may resolve against. Which of them roll is
decided by the builds named on the command line.

Omitted means every service on the cluster, which is correct while a cluster carries one
environment. Two targets naming different services in one cluster is how a staging service beside
production is addressed:

```toml
[[targets]]
name     = "public1"
cluster  = "tc-public1"
services = ["tc-public1-chatbot"]

[[targets]]
name     = "public1-stg"
cluster  = "tc-public1"                      # the same cluster
services = ["tc-public1-chatbot-stg"]
```

A container name carried by two services inside one target's scope is a hard error naming both. A
service the target names and the cluster does not have is also an error naming it.

## What is deliberately not in this file

**`.meimei.toml` declares only what is true of the repository, the same for every environment.**
Anything that varies per target and *can be asked of AWS* is read when it is needed.

Which ECS task definition a build is packed into is the case that set the rule. It is a per-cluster
decision made for that cluster's instance and ENI budget, so a copy here would be a global mirror of
a per-target fact and would drift the first time production packed differently from dev1. meimei
reads it from the cluster instead: the container names in a service's newest revision are the builds
it carries.

The task definition family is not here either, and is not derived. It is read out of the service's
task definition ARN, which is where ECS states it.

So before adding a key, apply the test: not "does it vary per target" but **"could AWS be asked".**
If AWS could be asked, ask it.

## Version 1 → version 2

Three edits:

1. Add `version = 2` as the first line.
2. Rename every `[[services]]` to `[[builds]]`.
3. Add `repository` and `container` to every build.

To reproduce version 1 behaviour exactly, `repository` is the old `project.name` + `-` + the build's
name, and `container` is the build's name. Nothing else in the file changes.

meimei refuses a version 1 file with those three edits named, and refuses a version higher than it
reads by telling you to upgrade. Both messages name the file by absolute path, which matters as soon
as `--config` is in play.
