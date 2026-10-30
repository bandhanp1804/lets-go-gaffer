# Contributing

Thanks for wanting to help. This is a personal project, run in the open, with a live site at [letsgogaffer.tech](https://letsgogaffer.tech). Contributions are welcome, and these rules keep them easy to review and safe to ship.

## Before you start

- **Small fixes** (a bug, a typo, a failing edge case): open a pull request directly.
- **Anything larger** (a feature, a new page, a change to the model, the assistant or the deployment): open an issue first and describe what you want to change and why. A short discussion saves you from writing something that cannot be merged.
- **Security problems:** do not open an issue or a pull request. Follow [SECURITY.md](SECURITY.md).

## Set up

Follow [Start the app](README.md#start-the-app) in the README. You need Go (the version in `go.mod`), Docker, and Node 20.9 or newer (23.6 or newer to run the web tests). No API keys are needed for most work: without them the assistant and news search are simply unavailable, and the tests use fakes.

## The rules

These are the project's design rules. A pull request that breaks one will be asked to change.

1. **The LLM never does maths or checks rules.** Expected points, lineups, transfers, chips, prices and FPL's squad rules are worked out in Go. The assistant only narrates the results of tools and retrieves news. Do not add a prompt that asks the model to calculate or validate something; add a Go function and a tool instead.
2. **Standard library first.** Do not add a Go module or an npm package without agreeing it in an issue first. Say what it does that the standard library cannot.
3. **Small interfaces, defined where they are used**, not next to their implementations.
4. **Wrap errors with context:** `fmt.Errorf("load squad %d: %w", id, err)`. Never discard one silently.
5. **Every network or database call takes a `context.Context` as its first argument.**
6. **Every goroutine has a clear way to stop**: a context, a closed channel, or a `WaitGroup` its owner waits on.
7. **Tests are table-driven.** A bug fix comes with a test that fails without the fix.
8. **Respect Fantasy Premier League's data.** Use its public endpoints politely: through the existing rate-limited client, with no bulk collection of other managers' teams or personal data. Never commit FPL login tokens.
9. **No secrets in the repository**: no keys, tokens, `.env` files, Terraform state or `terraform.tfvars`. Secret scanning blocks known key formats on push; the rest is on you.
10. **Explain the non-obvious.** A comment says *why*, not what. Match the style of the code around it.

## Before you open a pull request

Run what CI runs:

```bash
gofmt -l .                        # must print nothing
go vet ./...
go test -race ./...               # database tests skip without GAFFER_TEST_DSN: see docs/development.md
cd web && npx tsc --noEmit && npm test
```

If you changed a Go response struct, regenerate the TypeScript types and commit the result ([development.md](docs/development.md)):

```bash
go run ./cmd/gentypes
```

If you changed anything under `deploy/`, also run `terraform fmt -check -recursive deploy/terraform` and `helm lint deploy/helm/gaffer --set host=ci.example --set acmeEmail=ci@example.com`.

Update the documentation in `docs/` when behaviour changes: the docs describe what the code does, so they must not fall behind it.

## Pull requests

- **One change per pull request.** Keep refactors apart from features.
- **Branch from the latest `main`.** It must be up to date before merging.
- **Write a description** that says what changed, why, and how you checked it. Screenshots help for anything visible.
- **Commit messages:** a short summary line in the imperative ("Add …", "Fix …"), then a body that explains why. Pull requests are squash-merged, so the pull request's title becomes the commit on `main`.
- **CI must pass.** The `ci / go`, `ci / web` and `ci / deploy-files` checks are required. A maintainer approves workflow runs for pull requests from forks before they start, so expect a short wait on your first push.
- **Fork pull requests get tests only.** The preview environment, the Terraform plan and deploys run only for branches of this repository, because they have access to the cloud account. A maintainer checks visible changes on a preview before merging.

## What is out of scope

- Changes that make the live site cost more to run (bigger nodes, more replicas, a paid service) without an issue agreeing it first.
- Scrapers, or anything that collects data about other FPL managers.
- Rewrites in another language or framework.

## Conduct

Be respectful and assume good intent. Disagree about the code, never the person. Harassment of any kind gets you removed from the project.

## Licence

By contributing you agree that your contribution is licensed under the project's [MIT licence](LICENSE).
