# The vocabulary does not match AWS, and that is why the gap was hard to see

**Status:** finding only, nothing built. Observed on 2026-08-21 while trying to add a staging
environment to `jjc_manualchatbot` and failing to describe the problem in meimei's own words.

Three of meimei's core nouns already mean something different in AWS, and one of them sits at the
wrong level of the hierarchy. The feature gap in
[gap-many-environments-one-cluster.md](gap-many-environments-one-cluster.md) is downstream of this: it
is a naming bug before it is a missing feature, and renaming two fields makes it self-evident.

**Where it comes from.** `CLAUDE.md` records that v1 "deployed zip bundles to AWS CodeDeploy,
cataloguing builds in DynamoDB". v2 promotes container images onto ECS. Several words survived the
rewrite with their v1 meanings attached, and CodeDeploy's object model is not ECS's.

## What each name actually refers to

Declared in config:

| meimei calls it | AWS calls it | Who decides it |
|---|---|---|
| `project.name` | prefix of an **ECR repository name** | you, in `.meimei.toml` |
| `registry.account` + `region` | the **ECR registry** — `<acct>.dkr.ecr.<region>.amazonaws.com` | you; asserted against the STS caller identity |
| `registry.profile` | an **AWS shared-config profile**, not an API object | you |
| `[[services]].name` | **two** things at once: the ECR **repository** suffix, and a **container definition** `name` inside a task definition | you — but the container half has to match what Terraform wrote |
| `[[services]].platform` | the image's **OCI platform** | you |
| `[[targets]].name` | nothing in AWS — a local label for `--to` | you |
| `[[targets]].cluster` | the **ECS cluster** name | Terraform |
| `--tag` | an **ECR image tag** | you; defaults to `sha-<commit>` |

Discovered from the cluster at deploy time:

| meimei calls it | AWS calls it | Who decides it |
|---|---|---|
| `Task` (struct) | an **ECS service**, its **task definition family**, and the **container definitions** of that family's newest revision | Terraform; meimei only reads it |
| `Task.Service` | the **ECS service name**, *assumed* identical to the **task definition family** | Terraform's naming convention |
| `Task.Deployable` | newest **task definition revision** of the family | whoever called `RegisterTaskDefinition` last |
| `Task.Running` | the **revision** the service is actually running | the last `UpdateService` |
| `Task.Containers` | **container definition** names | Terraform |
| `Task.Desired` / `RunningCount` | the service's **desired count** and **running count** | Terraform / ECS |
| `Packing`, `byContainer` | nothing — meimei's own index | derived per deploy |
| `Swap` | an edit to one container definition's **`image`** field | meimei |
| `Promote` | **RegisterTaskDefinition** followed by **UpdateService**; AWS calls the result a **deployment** | meimei |
| "rollout", in the output | an ECS **deployment**, and its `rolloutState` | ECS |

## The three collisions

**`Task`.** meimei's `Task` is a service plus a family plus a container list. An AWS *task* is one
ephemeral running copy of a revision, with its own ARN. meimei never addresses one — `UpdateService`
replaces them on its behalf. The struct's own comment has to talk the reader out of the AWS meaning
("Task is one ECS service on the cluster"), which is the tell that the name is wrong.

**`Service`.** In config, a service is an image and a container name. In AWS, a service is the
controller that keeps tasks running and registers them with a load balancer. Both meanings appear
inside a single deploy, one from each vocabulary, and the code has to keep them straight by convention
alone.

**`Target`.** The CodeDeploy fingerprint. In CodeDeploy a *deployment target* is the thing being
deployed to — an instance, a Lambda, an ECS service. That is what the word meant in v1. In v2 it
quietly came to mean the **cluster** the thing lives in, which is a level higher. There is also a live
collision with ELB, where **target** and **target group** are the registration this same deploy
touches when new tasks come up healthy.

Minor: **`Packing`** brushes against ECS's `binpack` *placement strategy*, which is about fitting
tasks onto instances — nothing to do with which containers share a task definition.

Not a collision, just a leftover: **`catalog`** described v1's DynamoDB record of built artefacts. In
v2 it answers "what can this checkout build", which is a reasonable meaning for the word but not the
one it was given.

## Why this hides the gap

The hierarchy has six levels. Config names the first and the last, and infers the middle:

| Level | Example | Named where |
|---|---|---|
| ECS cluster | `tc-public1` | **config** — `[[targets]].cluster` |
| ECS service | `tc-public1-chatbot` | *inferred* |
| task definition family | `tc-public1-chatbot` | *inferred, assumed equal to the service* |
| task definition revision | `tc-public1-chatbot:7` | created by the deploy |
| container definition | `chatbot` | **config** — `[[services]].name` |
| task (running copy) | an ephemeral ARN | never addressed |

**Nothing names the ECS service.** It is back-inferred by asking every service in the cluster which
containers it carries, and matching on the container name. That works while container names are unique
cluster-wide, and breaks silently when they are not — which is the whole of the other gap.

The reason this was hard to *describe* is that meimei has no word for the level it is missing. Asking
"which service does `--to` point at?" gets the answer "the cluster", because `Target` is holding the
place where a service name would go.

## The rename that makes it obvious

Two fields, and the config starts reading as what it is:

- `[[services]]` → **`[[images]]`**. Its own doc comment already says "one buildable container image".
  That is an image and the container name it takes inside a task, which is a thing worth having a
  single name for — but "service" is the one name it must not be, because ECS has one.
- `[[targets]]` → **`[[clusters]]`**, or keep `target` as the user-facing label for `--to` while the
  field it holds is honestly called a cluster.

Read it back after that: *here are my images, here are my clusters.* The next question asks itself —
**where do I say which ECS service?** — and the answer today is that you cannot, which is the feature
gap stated in one line.

## One assumption worth making explicit while renaming

`Task.Service` is documented as `== the task definition family`. That is true because the Terraform in
these repos names both from one string, not because ECS requires it. A deploy needs both: it calls
`RegisterTaskDefinition` against the **family** and `UpdateService` against the **service**. Anywhere
the convention does not hold, meimei needs two names where it currently has one — so if the structs
are being renamed anyway, that is the moment to split the field rather than to re-document the
assumption.

## What must not be broken on the way through

Recorded in the other doc too, because it is the thing a rename is most likely to disturb: **nothing
in meimei matches by prefix or substring.** The repository name is exact concatenation passed straight
to `RepositoryName`, container resolution is an exact map key, and there is no `--family-prefix`, no
`HasPrefix` and no `Contains` on any AWS identifier. A service `foo` cannot reach `foobar`'s repository
or task in either direction. Any new resolution — especially one that grows a level to name the ECS
service — must stay exact.
