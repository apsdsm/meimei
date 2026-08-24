# `.meimei.toml` reference

**This documents config version 2.** Version 2 is what the vocabulary rename produces — see
[plan-rename-vocabulary.md](plan-rename-vocabulary.md). It is not what meimei 1.0.0 reads.

| Config version | meimei versions that read it | Top-level image table |
|---|---|---|
| 1 (no `version` key) | 1.0.0 | `[[services]]` |
| 2 | 1.1.0 onward | `[[images]]` |

**There is no backwards compatibility and none is planned.** A binary reads one config version and
refuses every other one by name. Two config files exist in the wild (`acme_main`,
`nova_chatbot`); both get migrated when 1.1.0 is tagged.

This page is the answer to "what does a correct file look like". Keep it current as the shape changes
— a reference that lags the loader is worse than no reference, because it is the thing someone copies
from.

## The `version` key

```toml
version = 2
```

Required, and it must be the first line.

TOML requires bare top-level keys to appear before the first `[table]` header, so a format version
declared at the top level puts itself at the top of the file. That is where it belongs: it decides how
everything below it is read, and it is not a property of the project, the registry or any target.

Three outcomes, all of them before any other validation:

| File says | meimei does |
|---|---|
| `version = 2` | reads it |
| no `version` key, or `version = 1` | refuses, naming the v1 → v2 edits |
| `version = 3` or higher | refuses, saying to upgrade meimei |

The refusals, verbatim:

```
Error: /home/nick/Code/acme_main/.meimei.toml: this file is version 1 (it has no version key)

  meimei reads version 2 only. Two edits:
      add     version = 2      as the first line
      rename  [[services]]  →  [[images]]

  nothing else in the file changes.
```

```
Error: /home/nick/Code/acme_main/.meimei.toml: this file is version 99, and this meimei reads version 2

  upgrade:  go install github.com/apsdsm/meimei@latest
```

Both name the file by absolute path, which matters as soon as `--config` is in play — the file being
refused is not always the one you would find by searching upwards. `version = 1` written explicitly
gets the first message too, with "it says version = 1" in place of "it has no version key". Neither
message hardcodes a meimei version, so neither goes stale.

**Why a version key rather than sniffing for the old table name.** Sniffing answers "is this the file
I expected" only for the changes already made. A version answers it for every change after this one,
including ones that add a key rather than rename one — which sniffing cannot see at all, because
`toml.Unmarshal` ignores keys it does not know. The point of the key is that the next breaking change
costs one integer and one error message.

## A complete file

Every key, with the multi-image and multi-target shape. `nova_chatbot` is the same file with one
`[[images]]` and one `[[targets]]`.

```toml
version = 2

[project]
name     = "acme"                # prefixes every ECR repository: acme-api, acme-user-web-spa, …
region   = "ap-northeast-1"
platform = "linux/arm64"         # every cluster here runs on Graviton

# One buildable container image per table. `name` is its identity everywhere:
# the repository suffix, the container name inside its ECS task, and what you
# type at the CLI.
[[images]]
name       = "api"
short      = "aa"                # optional abbreviation, for a future keyboard shortcut
dockerfile = "services/api/acme_system_api/Dockerfile"
group      = "api"               # presentational only — arranges the list on screen
color      = "blue"

[[images]]
name       = "user-web-spa"
short      = "as"
dockerfile = "services/web/acme_user_web_spa/Dockerfile"
context    = "."                 # the default; the build root, not the Dockerfile's directory
group      = "web"
color      = "green"

# Where images are pushed. ONE registry for every target: an image is built once
# and deployed anywhere, so it is not per-environment.
[registry]
account = "111122223333"         # asserted against the live caller identity before any push
profile = "acme-shared"          # AWS SSO profile; empty means ambient credentials (CI)
# region falls through to project.region

# Somewhere images get deployed to. A target is a cluster and, once the
# target-scoping field lands, which of its ECS services this target addresses.
[[targets]]
name    = "dev1"                 # the label you pass to --to
account = "444455556666"
profile = "acme-dev"
cluster = "acme-dev1-cluster"
```

## Every key

### Top level

| Key | Required | Default | What it is |
|---|---|---|---|
| `version` | yes | — | config format version. Must be `2`. |
| `[project]` | yes | — | one table |
| `[[images]]` | yes, at least one | — | one table per buildable image |
| `[registry]` | no | absent | required in practice for `build --push` and `deploy` |
| `[[targets]]` | no | none | `deploy` fails at run time if none is declared |

### `[project]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `name` | yes | — | prefix of every ECR repository name: `<name>-<image.name>` |
| `region` | no | empty | the default for `registry.region` and every `target.region` |
| `platform` | no | `linux/arm64` | the default for every `image.platform` |

### `[[images]]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `name` | yes | — | the repository suffix, the ECS container definition name, and the CLI word. Unique across the file. |
| `dockerfile` | yes | — | path relative to the build root. Not absolute, and may not escape the root. |
| `context` | no | `"."` | docker build context, relative to the build root. Same two restrictions. |
| `platform` | no | `project.platform` | the image's OCI platform |
| `short` | no | empty | abbreviation for a future keyboard shortcut. Unique across the file if set. |
| `group` | no | empty | presentational only. Arranges the list on screen and carries no deployment meaning. |
| `color` | no | empty | display colour |
| `disabled` | no | `false` | excluded from `--all` |

`name` doing three jobs at once is the coupling behind
[gap-many-environments-one-cluster.md](gap-many-environments-one-cluster.md). It is unchanged in
version 2.

### `[registry]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `account` | yes, if the table is present | — | AWS account id. Declared rather than discovered, and asserted against the live caller identity before any registry call — repositories here are immutable, so a push into the wrong account cannot be taken back. |
| `region` | yes, if the table is present | `project.region` | ECR region |
| `profile` | no | empty | AWS shared-config profile. Empty means ambient credentials. |

The registry host is `<account>.dkr.ecr.<region>.amazonaws.com`.

### `[[targets]]`

| Key | Required | Default | What it is |
|---|---|---|---|
| `name` | yes | — | the label `--to` takes. Nothing in AWS. Unique across the file. |
| `cluster` | yes | — | ECS cluster name. Spelled out rather than derived, because the convention differs per product (`acme-dev1-cluster` against `nova-public1`). |
| `account` | yes | — | AWS account id, asserted before the deploy runs |
| `region` | yes | `project.region` | the cluster's region |
| `profile` | no | empty | AWS shared-config profile |

`--to` is optional when exactly one target is declared, and required as soon as there are two — at
which point the error names them all.

## What is deliberately not in this file

The rule, from `CLAUDE.md`: **`.meimei.toml` declares only what is true of the repository, the same
for every environment.** Anything that varies per target is read from AWS when it is needed.

The case that set the rule: which ECS task an image is packed into is a per-cluster Terraform decision
made for that cluster's instance and ENI budget. A copy here would be a global mirror of a per-target
fact and would drift the first time production packed differently from dev1. meimei asks the cluster
instead — an ECS service's name is its task definition family, and its container names are the images
in it.

So before adding a key: if it describes how something is *deployed*, check whether Terraform already
owns it and whether the AWS API can be asked.

## Version 1 → version 2

Two edits, and nothing else:

1. Add `version = 2` as the first line.
2. Rename every `[[services]]` to `[[images]]`.

No key inside any table changes, and `[project]`, `[registry]` and `[[targets]]` are untouched. What
changed is the word: ECS has a `service` and an entry in this file is not one — it is one buildable
container image and the container name it takes inside a task. The reasoning is in
[gap-vocabulary.md](gap-vocabulary.md).

## Where the rules live

| Rule | File |
|---|---|
| the structs and every `toml:` key | `internal/config/config.go` |
| defaults filled after decoding | `LoadFrom`, same file |
| the version check | `LoadFrom`, before `Validate` |
| everything the file can be wrong about on its own | `Validate`, same file |
| whether a Dockerfile actually exists | **not** validated here — the catalog reports it per image, because it is a fact about the working tree and one missing file must not stop you seeing the other four |
