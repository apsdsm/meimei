# meimei documentation

The entry point. **Read this, then zoom in** — each page below has a one-line hook so you can judge
relevance without opening it. Nothing here restates the pages it points at.

`README.md` describes v1 (CodeDeploy, S3 bundles, DynamoDB) and is **stale on the `v2` branch**.
`CLAUDE.md` is the current intent and the rules that shape the rewrite.

| Page | What it covers |
|---|---|
| [gap-deploy-image-preflight.md](gap-deploy-image-preflight.md) | **Implemented.** `deploy` asks the registry whether every image it would promote is there, and refuses before registering anything if one is not — a commit-then-deploy without a build used to cost two minutes and a `CannotPullImageManifestError` naming neither the tag nor the cause. Also refuses a tag it cannot tell apart from another name for the same image. Keeps the original observation, the requirements, and what the tool must not regress. |
| [gap-vocabulary.md](gap-vocabulary.md) | **Finding, nothing built.** Three of meimei's core nouns already mean something else in AWS - `Task` is a service plus a family, `Service` is an image, and `Target` is a cluster wearing CodeDeploy's word for the thing being deployed to. Tables every name against what AWS calls it and who decides it. The point: nothing in config names the ECS service, so renaming two fields turns the other gap from a feature request into an obvious hole. |
| [gap-many-environments-one-cluster.md](gap-many-environments-one-cluster.md) | **Not built, and the naive shape is dangerous.** `deploy` cannot address two environments of one service in a single cluster: resolution is a flat container-name-to-task map, a collision overwrites silently, and alphabetical order decides the winner - so a staging service beside production makes `deploy chatbot` roll staging and report success while production is untouched. Has the proposed fix, and the exact-matching behaviour any fix must not regress. |
| [push-and-deploy-spec.md](push-and-deploy-spec.md) | A real ECR push and ECS deploy run by hand, command by command with actual output — the worked example `--push` and `deploy` were built from, and now the record of what they encode. Includes the six traps it hit — immutable tags, the emulated-build credential session, the fields `register-task-definition` refuses, and why the service was three revisions behind. |
| [known-issues.md](known-issues.md) | What has been found in use and not yet fixed, with what it was observed on. Today: the packing is read from the running revision while the deploy is built from the newest one, so the task-mate warning and the "no such container" refusal can both describe a task nobody is deploying. |
