# Sign-in: GitHub, email code or Google

Anyone can sign in, so recruiters and hiring managers can explore the app without being added first:

```text
browser ─▶ oauth2-proxy ──OIDC──▶ Dex  (https://<host>/dex: a page with both choices)
                                   ├─ GitHub                → the GitHub OAuth app
                                   └─ Email code or Google  → Microsoft Entra External ID
```

- **oauth2-proxy** guards every page and API call, as before, but now asks **Dex** instead of GitHub.
- **Dex** is a small sign-in hub in the cluster that offers both choices. It is needed because Entra External ID cannot sign people in with GitHub (GitHub's login is not standard OpenID Connect).
- **Entra External ID** (Microsoft's sign-in service for customers, separate from the Entra ID you log in to Azure with) offers an email one-time code and Google. It is free up to 50,000 monthly active users.
- Every sign-in reaches the app with an email address, so one person is one user however they sign in, and their saved team and token follow them.
- Everyone except the owners (`GAFFER_OWNERS`) has a daily limit on the parts that spend API credits; see [Limits](#limits).

## One-time setup (yours, in the browser)

These steps create identities and grant permissions, so they are done by you, not by scripts. Keep the values marked **→ save** for `deploy/azure.env`.

The host below is `gaffer.52-162-179-134.sslip.io`; use yours if it differs (`terraform -chdir=deploy/terraform output -raw host`).

### 1. Create the External ID tenant

1. Open the [Microsoft Entra admin center](https://entra.microsoft.com), signed in as you.
2. **Entra ID** → **Overview** → **Manage tenants** → **Create** → **External** → **Continue**.
3. Name it (for example `Let's Go Gaffer`), pick a domain name such as `letsgogaffer` (it becomes `letsgogaffer.ciamlogin.com`), and choose the **United States** region.
4. On **Add a subscription**, choose **Azure for Students** and the resource group `gaffer-rg`. (A free trial tenant also works, but it expires.)
5. Create it (a few minutes), then **switch to the new tenant** (top-right settings → Directories).
6. From its **Overview**, copy the **Tenant ID**. **→ save as `ENTRA_TENANT_ID`**

### 2. Google sign-in

1. In the [Google Cloud console](https://console.cloud.google.com), create a project (for example `lets-go-gaffer`).
2. **APIs & Services** → **OAuth consent screen**: user type **External**, app name `Let's Go Gaffer`, your email as support and developer contact. Under **Authorized domains** add `ciamlogin.com` and `microsoftonline.com`. **Publish** the app (testing mode only lets listed test users in).
3. **Credentials** → **Create credentials** → **OAuth client ID** → **Web application**. Add these **Authorized redirect URIs**, exactly as [Microsoft lists them](https://learn.microsoft.com/en-us/entra/external-id/customers/how-to-google-federation-customers): `<tenant-ID>` is the Tenant ID from step 1 and `<tenant-subdomain>` the domain name you chose (`letsgogaffer`). Note which lines use which.
   - `https://login.microsoftonline.com`
   - `https://login.microsoftonline.com/te/<tenant-ID>/oauth2/authresp`
   - `https://login.microsoftonline.com/te/<tenant-subdomain>.onmicrosoft.com/oauth2/authresp`
   - `https://<tenant-ID>.ciamlogin.com/<tenant-ID>/federation/oidc/accounts.google.com`
   - `https://<tenant-ID>.ciamlogin.com/<tenant-subdomain>.onmicrosoft.com/federation/oidc/accounts.google.com`
   - `https://<tenant-subdomain>.ciamlogin.com/<tenant-ID>/federation/oauth2`
   - `https://<tenant-subdomain>.ciamlogin.com/<tenant-subdomain>.onmicrosoft.com/federation/oauth2`
4. Copy the client ID and secret.
5. Back in the Entra admin center (external tenant): **Entra ID** → **External Identities** → **All identity providers** → **Built-in** tab → **Google** → **Configure** → paste them → **Save**.

### 3. Email one-time code

**External Identities** → **All identity providers** → **Built-in** → **Email accounts** → **Configure** → choose **Email with one-time passcode** → **Save**.

### 4. The app Dex signs in through

1. **Entra ID** → **App registrations** → **New registration**: name `gaffer-dex`, **Accounts in this organizational directory only**, redirect URI **Web** `https://gaffer.52-162-179-134.sslip.io/dex/callback` → **Register**.
2. Copy the **Application (client) ID**. **→ save as `ENTRA_CLIENT_ID`**
3. **Certificates & secrets** → **New client secret** (24 months) → copy the **Value**. **→ save as `ENTRA_CLIENT_SECRET`**
4. **Token configuration** → **Add optional claim** → **ID** → tick **email** → **Add** (accept the Microsoft Graph permission it offers).
5. **API permissions**: make sure `openid`, `profile`, `email` and `offline_access` (Microsoft Graph, delegated) are listed → **Grant admin consent**.

### 5. The sign-in flow

1. **External Identities** → **User flows** → **New user flow**: name `signup_signin`.
2. **Identity providers**: tick **Email one-time passcode** and **Google**.
3. **User attributes**: **Display Name** → **Create**.
4. Open the flow → **Applications** → **Add application** → `gaffer-dex`.

### 6. GitHub: point the existing OAuth app at Dex

GitHub → **Settings** → **Developer settings** → **OAuth Apps** → your app → **Authorization callback URL**: `https://gaffer.52-162-179-134.sslip.io/dex/callback` → **Update**. (Sign-in breaks between this step and the deploy, so do it last, just before running Deploy.)

### 7. Save the values

Add to `deploy/azure.env`:

```sh
ENTRA_TENANT_ID=<from step 1>
ENTRA_CLIENT_ID=<from step 4>
ENTRA_CLIENT_SECRET=<from step 4>
# Unlimited users: comma-separated emails (yours and Kiran's).
GAFFER_OWNERS=you@example.com,kiran@example.com
```

Then rerun `deploy/scripts/ci-setup.sh` to copy them into GitHub, and run **Deploy**.

## Limits

Each signed-in user other than the owners has a daily budget, reset at midnight UTC:

| What | Default | Setting |
|---|---|---|
| Ask questions | 15 | `GAFFER_LIMIT_ASK` |
| News searches | 40 | `GAFFER_LIMIT_SEARCH` |
| News refreshes | 3 | `GAFFER_LIMIT_REFRESH` |

Past the limit the API answers `429` with a plain message, and the Setup page shows what is left today. Counts are kept in Postgres (`usage_counts`), so every API pod shares them. Owners (`GAFFER_OWNERS`) and the local user on your own computer are never limited.
