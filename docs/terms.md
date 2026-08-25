# Terms

One word per concept, in config, in code, and in everything meimei prints.

The rule when adding a word: **if AWS has a name for the thing, use AWS's name.** meimei talks to AWS
about AWS objects, and an operator reading meimei's output next to the console should not have to
translate. Invent a word only where AWS has no object to name.

## The list

| Concept | Use | What AWS calls it |
|---|---|---|
| a thing this repository can produce, declared in `[[builds]]` and named at the CLI | **build** | nothing — meimei's own |
| what a build produces: an artefact in a repository, identified by a tag | **image** | an image |
| the slot inside a task definition an image is deployed into | **container** | container definition |
| the ECS controller that keeps tasks running and registers them with the load balancer | **service** — write *ECS service* on first use in any passage that also says *image* | service |
| the versioned shape a service runs | **task definition** | task definition |
| the name of a task definition, without a revision number | **family** | task definition family |
| one numbered task definition | **revision** | revision |
| one running copy of a revision | **task** | task |
| a place `--target` names: a cluster, and the services in it this target addresses | **target** | nothing — the ECS cluster it points at is a cluster |
| the ECS cluster a target names | **cluster** | cluster |
| the ECS services a target addresses | **scope** | nothing |
| registering a revision and pointing a service at it | **deploy** (verb), **promote** (the image onto the container) | ECS calls the result a deployment |
| the thing you watch afterwards | **rollout** | ECS's `rolloutState` |
| where images are pushed | **the registry**, or **ECR** when naming its account and region | ECR registry |
| what `[[builds]].repository` names | **repository** | ECR repository |
| the string that identifies one build of one image | **tag** | image tag |

## Words that mean something else here

**Label** means an OCI annotation on an image — `org.opencontainers.image.revision` and the rest. It
does not mean a tag. The flag that names a tag is `--tag`.

**Packing** is meimei's index from container name to the service carrying it. It has no AWS
counterpart. It brushes ECS's `binpack` placement strategy, which is about fitting tasks onto
instances and is unrelated.

**Catalog** answers "what can this checkout build". It is read from the config and the working tree,
with no AWS call.

## Build against image, service against task definition

**A build is not an image.** A build is a declaration: a Dockerfile, a context, a platform, and the
two AWS names it deploys under. It produces many images over its life, one per tag.

**An ECS service is not a task definition.** A service is a controller that keeps N copies running
and performs rollouts; a task definition is a versioned template. A service lives in a cluster; a
task definition lives in the account and region and can be run by services in different clusters.
In the repositories meimei was built for, Terraform names both from one variable, so the same string
appears in both roles — meimei reads the family out of the service's task definition ARN rather than
assuming they match.

## Style

**No `(s)` plurals.** Use the `plural(n, "image is", "images are")` helper.

**Lower case for meimei's own prose, AWS's capitalisation for AWS's identifiers.** `task definition`
in a sentence, `RegisterTaskDefinition` when naming the call.

## Enforcement

None automatic; no linter checks prose. This page is enforced by being the thing you read before
naming something.
