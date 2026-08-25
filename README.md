# meimei

A build and deploy CLI for containerised projects. Run it inside a project and it reads
`.meimei.toml` to learn what that repository can build, builds those images, pushes them to ECR, and
promotes them onto ECS.

<p align="center">
  <img src="meimei.jpeg" alt="meimei" width="400">
</p>

## Install

```bash
go install github.com/apsdsm/meimei@latest
```

Requires Go 1.25.5+, the `docker` CLI with buildx, and AWS credentials (SSO profiles are read from
your shared config).

## Quick start

1. Put a `.meimei.toml` in your project root — see [Configuration](#configuration).
2. Authenticate: `aws sso login --profile <the profile your config names>`.
3. See what the repo can build: `meimei ls`
4. Build and push: `meimei build api`
5. Promote onto a cluster: `meimei deploy api --target dev1`

## Configuration

`.meimei.toml` is found by walking up from the working directory, so meimei can be run from anywhere
inside a project. The directory holding it is the **build root**: every path in the file is relative
to that directory.

```toml
version = 2

[project]
name     = "acme"                # a label for this repo in output; names nothing in AWS
region   = "ap-northeast-1"
platform = "linux/arm64"         # default build platform

# One buildable container image per table. `name` is meimei's own label — what
# you type at the CLI. The two AWS-facing names are declared, never composed.
[[builds]]
name       = "api"               # what you type; names nothing in AWS
repository = "acme-api"          # exact ECR repository name. Required.
container  = "api"               # exact container definition name. Required.
short      = "aa"                # optional abbreviation, for a future keyboard shortcut
dockerfile = "services/api/Dockerfile"
context    = "."                 # default: the build root, not the Dockerfile's directory
group      = "api"               # presentational only — arranges the list on screen
color      = "blue"
platform   = "linux/amd64"       # optional, overrides project.platform for this build
disabled   = false               # keeps a declaration out of every build

# Where images are pushed. ONE registry for every target: an image is built once
# and deployed anywhere. Required unless every build uses --no-push.
[registry]
account = "111122223333"         # asserted against the live caller identity before any push
profile = "acme-shared"          # empty means ambient credentials, which is what CI has

# Somewhere images get deployed to: a cluster, and a scope inside it.
[[targets]]
name     = "dev1"                # the label you pass to --target
account  = "444455556666"
profile  = "acme-dev"
cluster  = "acme-dev1-cluster"
services = ["acme-dev1-internal", "acme-dev1-external"]   # optional; exact ECS service names
timeout  = "10m"                 # optional; how long to follow a rollout
# region falls through to project.region
```

`services` is what lets two targets share a cluster — a staging service beside production — and it
is a **scope, not an instruction**: naming two services does not make a deploy roll two services, it
makes those two the only ones a build name may resolve against. Omit it and the scope is the whole
cluster, which is right for a cluster carrying one environment.

[docs/config-reference.md](docs/config-reference.md) documents every key, its default, and what AWS
calls it.

### Older config files

A binary reads one config version and refuses every other one by name, naming the edits it needs.
There is no backwards compatibility and none is planned. The migration from version 1 is at the end
of [docs/config-reference.md](docs/config-reference.md).

## Commands

### `meimei ls`

Lists every image the config declares, what it builds from, and the tag a build would produce right
now. Local only; it makes no AWS calls. Exits non-zero if any image cannot be built, so a script can
gate on it.

| Flag | Description |
|---|---|
| `--json` | Machine-readable output |

### `meimei build [name...]`

Builds one or more of this repository's builds, or every buildable one with `--all`, **and pushes
the result to the registry**. A build ends at the registry: there is no point producing an image and
keeping it on the machine that made it, so pushing is not a flag.

| Flag | Description |
|---|---|
| `--all` | Build everything this repo declares |
| `--tag` | Tag with this release or ticket id instead of the commit |
| `--no-push` | Build without pushing — a diagnostic, not a way to keep the image |
| `--force` | Build from a dirty working tree |
| `--dry-run` | Print what would be built, and build nothing |

`--no-push` answers "does this build at all". It builds the same image a push would have sent — the
platform the config declares, not the host's — and loads it into the local docker image store. On an
x86 host an arm64 image loaded there will not run without emulation; that is accepted, because
`--no-push` is a diagnostic and not a way to get something runnable.

Images are tagged `sha-<commit>` unless `--tag` names something else. `sha-` is reserved for tags
meimei generates and refused as a `--tag`: ECR's lifecycle rule expires all but the newest `sha-*`
images, so a release in that namespace would be swept later.

A dirty working tree blocks a build with a commit tag, because tags are immutable and `sha-x` would
permanently name an image that is not commit x. Commit, pass `--tag`, pass `--force`, or use
`--no-push` to just check it builds.

Every build is given `--build-arg MEIMEI_BUILD_ID=<tag>`, carrying the same string the image is
tagged with, so code inside the container can report which build it is. A Dockerfile that does not
declare the `ARG` ignores it.

### `meimei deploy [name...]`

Points a target's ECS services at a different image.

A deploy copies the task definition that is registered, swaps the image on the named containers,
registers the result and rolls the ECS service. It never changes anything else about the task
definition — whatever manages your infrastructure owns the shape, and typically carries
`ignore_changes = [task_definition]` so the two do not fight.

| Flag | Description |
|---|---|
| `--all` | Deploy everything in this target's scope |
| `--tag` | Image tag to promote (default: the current commit) |
| `--target` | Target to deploy to (optional when the project declares one) |
| `--no-follow` | Trigger the rollout and return without waiting |
| `--dry-run` | Print what would be promoted, and change nothing |
| `--skip-image-check` | Promote without asking the registry whether the images are there |

`--all` means everything **this target runs**, not everything the repository declares. A build this
repo can make but this target does not carry is not part of "all" here.

Before registering anything it asks ECR whether every image it would promote is actually there, and
refuses with what is missing and what to run. A registry it cannot reach is a warning, not a
refusal.

Images packed into the same task definition are promoted in **one** revision and **one** rollout,
however many of them changed. That is also the only thing that works on a cluster brought up fresh,
where no task can start until every image in its definition is real.

### Global flags

| Flag | Description |
|---|---|
| `--config` | Path to `.meimei.toml`, overriding the directory-tree walk |

## Repository, container and tag names

Nothing is derived. Every AWS identifier is declared, and nothing in meimei matches one by prefix or
substring, so a build called `foo` cannot reach `foobar`'s repository or task in either direction.

**Repository** = `[[builds]].repository`, verbatim. A build called `api` may push to a repository
called anything at all; the project's name has nothing to do with it. meimei never creates a
repository, so this always names one that already exists.

**Container** = `[[builds]].container`, verbatim, matched exactly against the container definition
names in the task definition.

**Tag** — you name it, or meimei derives one from the commit. This is the one thing still computed,
and it names an artefact you just built rather than a piece of infrastructure.

| What you type | Tag you get |
|---|---|
| `meimei build api` | `sha-abc1234` — the short commit, because you did not name one |
| `meimei build api --tag my-cool-build` | `my-cool-build`, verbatim |
| `meimei build api --tag sha-anything` | refused |

`abc1234` is `git rev-parse --short HEAD`. The `sha-` in front is a literal prefix, and it is there
so ECR's lifecycle policy can match on it — keep the newest N images tagged `sha-*` and expire the
rest. A tag you name yourself carries no prefix, so that rule can never sweep it. Which is why a
`--tag` starting with `sha-` is refused: a release living in that namespace would be expired some
builds later, possibly taking out the image a live task definition still points at.

Together, that is the reference a task definition ends up carrying:

```
111122223333.dkr.ecr.ap-northeast-1.amazonaws.com/acme-api:sha-abc1234
└──────── registry ─────────────────────────────┘ └ repo ┘ └── tag ──┘
```

### What renaming affects

`name` is meimei's own label. Rename it and nothing in AWS is affected — it changes what you type
and what appears in output, and that is all.

`repository` and `container` are the two names that face AWS, and meimei creates neither. Point one
at something that does not exist and it fails **loudly, and before anything is changed**:

```
$ meimei build api
Error: repository "acme-api" does not exist in account 111122223333 (ap-northeast-1)
       — it is created by Terraform, not by meimei

$ meimei deploy api --target dev1
Error: build "api" wants a container named "web", and target dev1 does not run one —
       its scope has app, worker
```

The first is raised before docker builds anything. The second is raised before any revision is
registered.

(The first message names Terraform because that is what creates repositories in the projects meimei
was built for. It is inaccurate for a cluster built by hand, where the answer is simply that meimei
does not create repositories. It is on the list to reword.)

Two builds may share a `repository` under different `container` names, which is one image reaching
two environments. Two builds may not share a `name`.

## What build and deploy do

`meimei build api` builds the image, tags it, and pushes it to the repository the build declares. A
tag already in the repository is skipped rather than failing, because tags are immutable and a tag
already there is a build that already finished.

`meimei deploy api --tag T --target dev1` asks ECR whether the image is there, finds the ECS service
in the target's scope carrying the build's container, copies that family's newest revision with the
image swapped, registers it, points the service at it, and follows the rollout.

[docs/deploys.md](docs/deploys.md) has the step-by-step version and the AWS behaviour behind it.
Three things that are easy to picture wrongly:

**A task definition is never updated.** Revisions are immutable, so a deploy registers a new one that
is a copy with the image swapped. Rolling back is therefore an ordinary deploy: the old revision is
still there, and the service can be pointed back at it.

**The service is told, it does not poll.** Terraform can register five revisions and the service goes
on running the old one until something calls `UpdateService`.

**A service is a controller, a task definition is a template.** In these repositories Terraform names
both from one variable, so `acme-dev1-internal` is both an ECS service name and a task definition
family name — two different objects sharing a string. `RegisterTaskDefinition` is called against the
family, `UpdateService` against the service. meimei reads the family out of the service's task
definition ARN rather than assuming they match, so a cluster pairing them differently works too.

## What meimei requires of your infrastructure

meimei never creates an AWS resource. It reads a cluster, registers a task definition revision, and
points a service at it. Everything it deploys onto has to exist first.

**That does not mean it needs Terraform.** Terraform is what creates these resources in the repos
meimei was built for, and some error messages say so, but nothing in meimei depends on it. What it
needs is that certain strings line up. A cluster built by hand in the console works exactly the same
way.

**meimei requires no naming convention at all.** Every AWS name it uses is one you wrote in the
config, used verbatim. Nothing is composed from parts, and nothing matches by prefix or substring.

| Thing | What meimei requires | Who chooses |
|---|---|---|
| target `name` | nothing at all — meimei's own label, with no counterpart in AWS | **you, freely** |
| build `name` | nothing at all — meimei's own label | **you, freely** |
| `project.name` | nothing at all — it labels the repo in output | **you, freely** |
| `repository` | must name a real ECR repository, exactly | you, then declare it |
| `container` | must name a real container definition in the task definition, exactly | you, then declare it |
| `cluster` | must name a real ECS cluster, exactly | you, then declare it |
| `services` | must name real ECS services in that cluster, exactly | you, then declare it |
| `account` | must match the live caller identity, which is asserted before any call | forced |

A service the target names and the cluster does not have is an error naming it, not a silent
omission. A container name carried by two services in one target's scope is an error naming both.

The task definition family is neither declared nor derived — it is read out of the service's task
definition ARN, which is where ECS states it. So a service and its family may be named however you
like, including differently from each other.

### Assumed, but not required

| Assumption | What happens without it |
|---|---|
| ECR repositories use **immutable tags** | Everything still works, but "already pushed, skipping" will skip a push you meant to redo, and the dirty-tree guard is protecting against a lie that is now overwritable. |
| A lifecycle policy expiring `sha-*` | Nothing breaks; images accumulate. The prefix costs nothing on its own. |
| The cluster runs `linux/arm64` | Nothing breaks — it is only the default for `project.platform`. Set it to what your cluster actually runs. |

### Building it by hand

What has to exist before `meimei deploy` will work, and what to write in the config for each:

| Must exist | Config key |
|---|---|
| An ECR repository, named anything | `[[builds]].repository` |
| A task definition with a container definition in it, named anything. Its `image` can start as an unpullable placeholder — a first deploy replaces it. | `[[builds]].container` |
| An ECS service running that task definition, named anything | `[[targets]].services` |
| The cluster it runs in | `[[targets]].cluster` |
| Credentials for both accounts | `profile` in `[registry]` and on the target |

Once those exist, `meimei build api` and `meimei deploy api --target <target>` work with no further
setup. Every "named anything" above is meant literally: meimei reads the strings you gave it.

## How a deploy resolves what to touch

**Which services are this target's** is declared: `[[targets]].services`. ECS has no notion of an
environment, so nothing on the cluster says which of its services are production and which are
staging — that is the one fact about a deploy that cannot be discovered.

**What is in each service** is not declared. meimei asks the cluster: the container names inside a
service's newest revision are the builds it carries. So a decision made for one cluster's instance
and ENI budget cannot drift out of a copy in a config file.

A container name carried by two services in one target's scope is a **hard error naming both**.
There is no correct choice: both services are this target's, and a deploy naming that container
cannot say which was meant.

**The newest revision, not the running one.** Under `ignore_changes = [task_definition]`, Terraform
registers a revision and leaves the service where it was, so the shape a deploy carries forward is
the registered one. Copying the running revision instead would silently discard every shape change
Terraform has made since the last deploy.

Two targets may therefore point into the same cluster with different `services`, which is how a
staging service beside production is addressed:

```toml
[[targets]]
name     = "prod"
cluster  = "nova-public1"
services = ["nova-public1-chatbot"]

[[targets]]
name     = "stg"
cluster  = "nova-public1"          # the same cluster
services = ["nova-public1-chatbot-stg"]
```

Both pull the same repository, so a tag verified in staging is the identical image promoted to
production. A container name carried by two services inside one target's scope is refused, naming
both.

## AWS access

AWS is reached through aws-sdk-go-v2, not the `aws` CLI. Docker is shelled out to.

The calling principal needs:

```
sts:GetCallerIdentity
ecr:GetAuthorizationToken
ecr:DescribeImages
ecr:BatchGetImage, ecr:PutImage, ecr:InitiateLayerUpload, ecr:UploadLayerPart, ecr:CompleteLayerUpload
ecs:ListServices, ecs:DescribeServices
ecs:DescribeTaskDefinition, ecs:RegisterTaskDefinition
ecs:UpdateService
iam:PassRole            (for the task and execution roles already on the task definition)
```

The registry account is asserted against the live caller identity before anything is sent, because
repositories here use immutable tags and an image pushed into the wrong account cannot be taken back.

## Documentation

[docs/index.md](docs/index.md) lists every page with a one-line description.

- [config-reference.md](docs/config-reference.md) — every `.meimei.toml` key
- [terms.md](docs/terms.md) — one word per concept
- [deploys.md](docs/deploys.md) — what `build` and `deploy` do, and the AWS behaviour they encode
- [decisions.md](docs/decisions.md) — why the design is what it is
- [known-issues.md](docs/known-issues.md) — what is wrong or missing

`CLAUDE.md` holds the rules that shape the code.
