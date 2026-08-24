# Terms

**One word per concept, in config, in code, and in everything meimei prints.** This page is the list.
It exists because three of meimei's nouns already meant something else in AWS — the finding is in
[gap-vocabulary.md](gap-vocabulary.md) — and because the same concept was being called two or three
different things inside one command's output.

The rule when adding a word: **if AWS has a name for the thing, use AWS's name.** meimei talks to AWS
about AWS objects, and an operator reading meimei's output next to the console should not have to
translate. Invent a word only where AWS has no object to name.

## The list

| Concept | Use | Never | What AWS calls it |
|---|---|---|---|
| a buildable container image, declared in the config | **image** | *service*, *container* | an ECR repository and a tag in it |
| the slot inside a task definition an image is deployed into | **container** | *service*, *image* | container definition |
| the ECS controller that keeps tasks running and registers them with the load balancer | **service** — write *ECS service* on first use in any passage that also says *image* | *task* | service |
| the versioned shape a service runs | **task definition** | *task* | task definition |
| the name of a task definition, without a revision number | **family** | *service*, *task* | task definition family |
| one numbered task definition | **revision** | *version* | revision |
| one running copy of a revision | **task** | — | task |
| a place `--to` names | **target** | *environment*, *cluster* | nothing — the ECS cluster it points at is a cluster |
| the ECS cluster a target names | **cluster** | *target* | cluster |
| registering a revision and pointing a service at it | **deploy** (verb), **promote** (the image onto the container) | *release*, *ship* | ECS calls the result a deployment |
| the thing you watch afterwards | **rollout** | *deployment* | ECS's `rolloutState` — *rollout* is AWS's own word here |
| where images are pushed | **the registry**, or **ECR** when naming its account and region | *repo host* | ECR registry |
| `project.name` + `image.name` | **repository** | *repo* | ECR repository |
| the string that identifies one build of one image | **tag** | *version* | image tag |
| what `--label` takes | **label** — the *input* a tag is computed from, not the tag | *tag* | nothing |

## The two collisions this list exists to stop

**`service` for a buildable image.** `meimei deploy` prints both meanings today. `no service named`
means "you did not name an image"; `service is on acme-dev1-internal:5, deploying from :7` means the
ECS service. Same word, same command, two levels of the hierarchy. `image` for the first is what frees
`service` for the second.

**`container` for a buildable image.** `meimei --help` says "Build and deploy this project's
containers" and `meimei ls --help` says "List the containers this project can build". Neither is an
image *or* an ECS container definition — it is a third word for a thing that already had two. A
container is the slot in a task definition, and nothing else.

## One consequence worth knowing

`image.name` is still one string doing three jobs: the repository suffix, the container name inside the
task, and what you type at the CLI. So `meimei deploy api` names an image at the CLI and is resolved as
a container name against the cluster, and both readings are correct because the strings are the same.

The CLI word is **image**, because that is what the config declares and what the operator is choosing.
When the coupling is split — the field that lets one image reach two environments in one cluster,
[gap-many-environments-one-cluster.md](gap-many-environments-one-cluster.md) — `deploy`'s positional
becomes a container name, and this page changes with it.

## Style, not vocabulary

Two things that are not synonyms but read as carelessness in the same output:

- **No `(s)` plurals.** `%d image(s)` becomes the `plural(n, "image is", "images are")` helper in
  `cmd/deploy.go`. Three sites use `(s)` today: `cmd/ls.go:50`, `cmd/build.go:149`,
  `cmd/build.go:166`, plus `internal/deploy/follow.go:192`.
- **Lower case for meimei's own prose, AWS's capitalisation for AWS's identifiers.** `task definition`
  in a sentence, `RegisterTaskDefinition` when naming the call.

## Where this is enforced

Nowhere automatically — no linter checks prose. It is enforced by this page being the thing you read
before naming something, and by the sweep in
[plan-rename-vocabulary.md](plan-rename-vocabulary.md) step 6, which lists every string that has to
change to make the list true.
