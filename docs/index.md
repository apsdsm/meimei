# meimei documentation

The entry point. **Read this, then zoom in** — each page below has a one-line hook so you can judge
relevance without opening it. Nothing here restates the pages it points at.

`README.md` describes v1 (CodeDeploy, S3 bundles, DynamoDB) and is **stale on the `v2` branch**.
`CLAUDE.md` is the current intent and the rules that shape the rewrite.

| Page | What it covers |
|---|---|
| [gap-deploy-image-preflight.md](gap-deploy-image-preflight.md) | **Not implemented.** `deploy` rolls a tag without asking the registry whether the image is there, so a commit-then-deploy without a build costs two minutes and a `CannotPullImageManifestError` that names neither the tag nor the cause. Observed twice in one afternoon. Includes the requirements, where the code goes, and the tests — plus what the tool already gets right and must not regress. |
| [push-and-deploy-spec.md](push-and-deploy-spec.md) | A real ECR push and ECS deploy run by hand, command by command with actual output — the worked example `--push` and `deploy` were built from, and now the record of what they encode. Includes the six traps it hit — immutable tags, the emulated-build credential session, the fields `register-task-definition` refuses, and why the service was three revisions behind. |
| [known-issues.md](known-issues.md) | What has been found in use and not yet fixed, with what it was observed on. Today: the packing is read from the running revision while the deploy is built from the newest one, so the task-mate warning and the "no such container" refusal can both describe a task nobody is deploying. |
