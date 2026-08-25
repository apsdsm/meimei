# meimei documentation

`README.md` is the starting point: what meimei is, how to configure it, and what it requires of your
infrastructure. `CLAUDE.md` holds the rules that shape the code.

| Page | What it covers |
|---|---|
| [config-reference.md](config-reference.md) | Every `.meimei.toml` key: whether it is required, its default, and what AWS calls it. Config version 2, with the version 1 migration at the end. |
| [terms.md](terms.md) | One word per concept — build, image, container, service, task definition, family, revision, task, target, cluster, scope, rollout, repository, tag. The rule it applies: if AWS has a name for the thing, use AWS's name. Read it before naming anything. |
| [deploys.md](deploys.md) | What `build` and `deploy` actually do, step by step; why a deploy copies the newest revision rather than the running one; what the registry preflight checks; and the AWS behaviour the two commands encode. |
| [decisions.md](decisions.md) | Why meimei is shaped the way it is, each entry with its reason and date. Declared rather than computed AWS names, targets as a cluster plus a scope, every build pushing, the flag rule, the vocabulary, and the config version scheme. |
| [known-issues.md](known-issues.md) | What is wrong or missing in the code as it stands, and which way each fix points. |
