# Deploying to Azure

The whole app runs on Azure Kubernetes Service, provisioned with Terraform and installed with Helm, behind a GitHub login. Every part is a Kubernetes deployment, and the web app, API and worker scale on CPU.

```
Browser ─https─▶ Traefik (one load balancer, sslip.io name, Let's Encrypt certificate)
                 └─ oauth2-proxy (GitHub login, allowed users only)
                     ├─ /          → web     Next.js, 1–3 pods
                     ├─ /api       → api     gaffer serve, 1–3 pods, sticky cookie
                     └─ /temporal  → Temporal UI
worker (gaffer worker, 1–2 pods) and api ─▶ Temporal (official chart) ─▶ Postgres
everything ─▶ Azure Postgres Flexible Server B1ms on a private subnet (pgvector)
```

## What it costs

Measured against the Azure retail price API in October 2026, North Central US. The student subscription allows only North Central US, Canada Central, Sweden Central, Norway East and Denmark East.

| Part | Price | On Azure for Students |
|---|---|---|
| AKS control plane | free | always free |
| Node, Standard_B2als_v2 (2 vCPU, 4 GB) | $27.45/mo each, 1–2 nodes | paid from the $100 credit |
| Node OS disk | about $5/mo each | paid |
| Load balancer and public IPs | about $25/mo | paid |
| Postgres Flexible B1ms, 32 GB | $13.50/mo list | **free for 12 months** |
| Container registry, Standard | list price | **free for 12 months** |

About $57/mo with one node, $90 with two. Stopped (`stop.sh`) it is about $25/mo: the load balancer, IP and disks still bill. A budget alert emails at 50% and 80% of $100.

The free student VM sizes (B1s, B2ats_v2, B2pts_v2) have 1 GB of memory, below the 4 GB an AKS node needs, so the nodes are paid. DigitalOcean was about $51/mo all in but has no student credit.

## First deploy

You need `az`, `terraform`, `kubectl`, `helm` and Docker with buildx (`brew install azure-cli helm`).

1. `az login`, then copy `deploy/terraform/terraform.tfvars.example` to `terraform.tfvars` with your subscription id (`az account show --query id -o tsv`) and an email for budget alerts.
2. `deploy/scripts/deploy.sh infra`. Terraform creates the network, cluster, registry, Postgres and budget (10–15 minutes) and prints the address, `https://gaffer.<ip>.sslip.io`.
3. Create a GitHub OAuth app: GitHub > Settings > Developer settings > OAuth Apps > New OAuth App, with homepage `https://<host>` and callback `https://<host>/oauth2/callback`. Copy `deploy/azure.env.example` to `deploy/azure.env` and fill in its id and secret, the GitHub usernames allowed in, and an email for Let's Encrypt.
4. `deploy/scripts/deploy.sh app`. It builds both images for linux/amd64 and pushes them to the registry, installs Traefik, cert-manager, Temporal and oauth2-proxy, creates the Kubernetes Secrets from `.env` and `deploy/azure.env`, and installs the app. A first-run job loads the season and starts a news pull.

Rerun `deploy.sh app` to roll out new code: images are tagged with the commit.

## GitHub Actions

| Workflow | When | What it does |
|---|---|---|
| CI (`.github/workflows/ci.yml`) | every pull request and push to main | Go tests against a pgvector Postgres, web typecheck and tests, `terraform fmt`/`validate`, `helm lint`. |
| Plan (`.github/workflows/plan.yml`) | every pull request | A read-only `terraform plan`, summarised on the run page. Its own workflow because it logs in to Azure, a permission the deploy's call to CI must not need. |
| Deploy (`.github/workflows/deploy.yml`) | Actions > Deploy > Run workflow, on main | CI, then `terraform apply`, the images, and the Helm rollout (`deploy.sh infra`, then `app`). |

The deploy is started by hand because GitHub's "required reviewers" approval needs a paid plan for a private repository; the click is the approval. On a public repository, add a `push` trigger and a reviewer on the `production` environment instead.

GitHub logs in to Azure with OIDC: short-lived tokens, no stored Azure password. Two identities, made once by `deploy/scripts/ci-setup.sh`:

- **gaffer-github-plan**, usable only from pull requests: Reader on the subscription, read-only on the Terraform state.
- **gaffer-github-deploy**, usable only from the `production` environment (main branch only): Contributor, write on the state, and allowed to grant exactly one role, AcrPull, which Terraform gives the cluster.

The app keys live in the `production` environment's secrets; the script copies them from `.env` and `deploy/azure.env` without printing them. Terraform state is in an Azure storage account (Entra ID access only, no keys), named in `deploy/terraform/backend.hcl`, so your laptop and CI share it and take turns through its lock.

## Day to day

| Command | What it does |
|---|---|
| `deploy/scripts/deploy.sh status` | Pods, autoscalers and the address |
| `deploy/scripts/stop.sh` | Stops the nodes to save credit; the address stays the same |
| `deploy/scripts/start.sh` | Starts them again; pods return in a few minutes |
| `deploy/scripts/destroy.sh` | Deletes everything, including the database |

## Secrets

Secrets never pass through Terraform state or Git. The database password is generated by Terraform (local, gitignored state) and goes straight into Kubernetes Secrets, as do the keys from `.env` (Anthropic, Voyage, ntfy, team id) and the GitHub OAuth credentials. `FPL_AUTH_HEADER` is left out: it lasts an hour.

## Design notes

- **Traefik, not ingress-nginx.** ingress-nginx was retired in March 2026 and gets no more security fixes. Traefik's forwardAuth asks oauth2-proxy about every request; the login pages themselves are the only unguarded path.
- **The API in public mode.** `gaffer serve` still refuses anything but loopback by default. In the cluster it runs with `-public -hosts <host>,gaffer-api`, so it answers only to the site's name and its own service name, and only behind the login.
- **Sticky API pods.** An Ask conversation lives in one API pod's memory, so a cookie keeps each browser on one pod. A pod that scales away ends its conversations; the page starts a new one.
- **A small database.** B1ms allows about 50 connections: the app's pools are capped at 5 each (`pool_max_conns` in the DSN) and Temporal's at 3 per store. Migrations take an advisory lock, so pods starting together never race.
- **Fixed address.** The ingress IP is a Terraform resource, so stopping and starting the cluster keeps the sslip.io name and the GitHub callback valid.
