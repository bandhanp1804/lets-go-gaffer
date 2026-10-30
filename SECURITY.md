# Security

## Reporting a vulnerability

Please report security problems privately, not in a public issue:

- through GitHub's **Report a vulnerability** button on this repository's **Security** tab, or
- by email to risingspy007@gmail.com.

Say what you found and how to reproduce it. You will get a reply within a week. Please give a fix a reasonable time before you disclose it publicly.

## In scope

The code in this repository and the site it runs, [letsgogaffer.tech](https://letsgogaffer.tech): for example a way to act as another signed-in user, read another user's saved settings or alerts, get past the daily limits, or reach the owner-only pages.

## Out of scope

- Denial of service, including load and rate testing against the live site.
- Problems in third-party services (GitHub, Google, Microsoft, Fantasy Premier League, Anthropic, Voyage, Datadog, Azure). Report those to them.
- Reports from automated scanners with no demonstrated impact.

## How secrets are handled

None are in this repository:
- **App keys:** live in GitHub environment secrets and Kubernetes Secrets.
- **Azure access:** Actions log in to Azure with OIDC, so no cloud credentials are stored at all.
- **Users' FPL tokens:** encrypted in the database (AES-256-GCM).

[docs/deploy.md](docs/deploy.md) and [docs/sign-in.md](docs/sign-in.md) have the details.
