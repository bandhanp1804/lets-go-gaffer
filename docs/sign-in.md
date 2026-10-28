# Sign-in: GitHub, Google or Microsoft

Anyone can sign in, so recruiters and hiring managers can explore the app without being added first:

```text
browser ─▶ oauth2-proxy ──OIDC──▶ Dex  (https://<host>/dex: a page with the choices)
                                   ├─ GitHub     → the GitHub OAuth app
                                   ├─ Google     → a Google OAuth client
                                   └─ Microsoft  → an Entra ID app registration (work, school and
                                                   personal Outlook/Hotmail accounts)
```

- **oauth2-proxy** guards every page and API call, as before, but asks **Dex** instead of GitHub.
- **Dex** is a small sign-in hub in the cluster that offers each provider whose credentials are set: GitHub always, Google and Microsoft once configured. A provider that cannot be reached only logs an error; the others keep working.
- Every sign-in reaches the app with an email address, so one person is one user however they sign in, and their saved team and token follow them.
- Everyone except the owners (`GAFFER_OWNERS`) has a daily limit on the parts that spend API credits; see [Limits](#limits).

**Why not Entra External ID?** It would add an emailed one-time code, but an External ID tenant is an Azure resource in a broad location ("United States"), and the Azure for Students policy only allows five specific regions, so creating one is refused. An ordinary Entra ID app registration is not a regional resource, so "Sign in with Microsoft" works without it.

## One-time setup (yours, in the browser)

These steps create identities and secrets, so they are done by you, not by scripts. Keep the values marked **→ save** for `deploy/azure.env`. The host below is `gaffer.52-162-179-134.sslip.io`; use yours if it differs (`terraform -chdir=deploy/terraform output -raw host`).

### 1. Google

1. In the [Google Cloud console](https://console.cloud.google.com), create a project (for example `lets-go-gaffer`) and open it.
2. **APIs & Services** → **OAuth consent screen** (Google Auth Platform): **External**, app name `Let's Go Gaffer`, your email as support and developer contact. Under **Authorized domains** add `sslip.io`. Scopes: only the default `openid`, `email` and `profile`. **Publish** the app (in testing mode only listed test users can sign in).
3. **Credentials** → **Create credentials** → **OAuth client ID** → **Web application** → **Authorized redirect URIs**: `https://gaffer.52-162-179-134.sslip.io/dex/callback` → **Create**.
4. Copy the client ID and secret. **→ save as `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`**

If Google will not accept `sslip.io` as an authorized domain, Google sign-in needs a domain you own (about $10 a year); GitHub and Microsoft work without one.

### 2. Microsoft

1. In the [Microsoft Entra admin center](https://entra.microsoft.com) (your usual directory), **Entra ID** → **App registrations** → **New registration**.
2. Name `Let's Go Gaffer`; **Supported account types**: **Accounts in any organizational directory and personal Microsoft accounts**; **Redirect URI**: **Web**, `https://gaffer.52-162-179-134.sslip.io/dex/callback` → **Register**.
3. Copy the **Application (client) ID**. **→ save as `MICROSOFT_CLIENT_ID`**
4. **Certificates & secrets** → **New client secret** (24 months) → copy the **Value**. **→ save as `MICROSOFT_CLIENT_SECRET`** (it is shown once)
5. **API permissions** should already list Microsoft Graph **User.Read** (delegated); nothing else is needed.

### 3. GitHub: point the existing OAuth app at Dex

GitHub → **Settings** → **Developer settings** → **OAuth Apps** → your app → **Authorization callback URL**: `https://gaffer.52-162-179-134.sslip.io/dex/callback` → **Update**. Sign-in breaks between this change and the deploy finishing, so do it last, right before running Deploy.

### 4. Save the values and deploy

Add to `deploy/azure.env`:

```sh
GOOGLE_CLIENT_ID=<step 1>
GOOGLE_CLIENT_SECRET=<step 1>
MICROSOFT_CLIENT_ID=<step 2>
MICROSOFT_CLIENT_SECRET=<step 2>
# Never limited: the emails you and Kiran sign in with (for GitHub, your primary GitHub email).
GAFFER_OWNERS=you@example.com,kiran@example.com
```

Then rerun `deploy/scripts/ci-setup.sh` to copy them into GitHub, do step 3, and run **Deploy**. Everyone signs in once more afterwards.

## Limits

Each signed-in user other than the owners has a daily budget, reset at midnight UTC:

| What | Default | Setting |
|---|---|---|
| Ask questions | 15 | `GAFFER_LIMIT_ASK` |
| News searches | 40 | `GAFFER_LIMIT_SEARCH` |
| News refreshes | 3 | `GAFFER_LIMIT_REFRESH` |

Past the limit the API answers `429` with a plain message, and the Setup page shows what is left today. Counts are kept in Postgres (`usage_counts`), so every API pod shares them. Owners (`GAFFER_OWNERS`) and the local user on your own computer are never limited.
