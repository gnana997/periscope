# Setting up Okta for Periscope

A condensed checklist. End state: a working Okta org + an `auth.yaml`
whose values you can drop into Periscope to get the
"Sign in with your IdP" → callback → dashboard flow working.

Companion file: `examples/config/auth.yaml.okta`.

---

## 1. What you need

- An Okta org (the free Developer Edition is fine).
- Org-level admin access — you'll create an OIDC Application and edit
  the Authorization Server.
- A URL Periscope will be reachable at. For local dev, that's
  `http://localhost:5173`. For prod, something like
  `https://periscope.your-corp.com`.

> Note: the Vite dev server proxies `/api/*` to the Go backend on
> `:8088`, so the browser-visible host stays `:5173` and Okta's
> redirect URI points there.

---

## 2. Create the Application

**Applications → Applications → Create App Integration**

- **Sign-in method**: **OIDC — OpenID Connect**
- **Application type**: **Web Application** (not SPA — Periscope's Go
  backend is the OAuth client)
- Click **Next**.

On the form:

- **App integration name**: `Periscope` (anything you like).
- **Grant type**: tick **Authorization Code** and **Refresh Token**.
  Untick everything else.
- **Sign-in redirect URIs**:
  ```
  http://localhost:5173/api/auth/callback
  https://periscope.your-corp.com/api/auth/callback
  ```
- **Sign-out redirect URIs**:
  ```
  http://localhost:5173/api/auth/loggedout
  https://periscope.your-corp.com/api/auth/loggedout
  ```
- **Controlled access**: pick whichever rule matches your org —
  typically "Limit access to selected groups" with a `periscope-users`
  group. (You'll create that group in 4 if it doesn't exist.)

Click **Save**.

---

## 3. Application credentials

Open the new app → **General** tab → scroll to **Client Credentials**:

- Note **Client ID** and **Client Secret** — these go into `auth.yaml`.
- **Client authentication**: `Client secret` (Post). The default.

---

## 4. Groups

**Directory → Groups** — create a group called `periscope-users` (or
whatever you'll list in `auth.yaml: allowedGroups`). Add yourself plus
the SREs who should have Periscope access.

Then on the Application:

**Applications → Periscope → Assignments → Assign → Assign to Groups**
→ pick `periscope-users` → Done.

---

## 5. Authorization Server: emit the `groups` claim

Okta exposes a default authorization server at `/oauth2/default`. If
you've never touched it, that's what you'll use. (If your org runs a
custom authorization server, edit that one instead — same procedure.)

**Security → API → Authorization Servers → default → Claims**

Look for an existing `groups` claim. If it's missing or filtered,
**Add Claim**:

- **Name**: `groups`
- **Include in token type**: **ID Token**, **Always**
- **Value type**: **Groups**
- **Filter**: `Matches regex` `.*` (or `Starts with` `periscope-` to
  scope tightly)
- **Include in**: **The following scopes** → tick `groups`

Save.

Optionally repeat for **Access Token** if you ever want backend
authorization off the access token. Periscope reads the ID token first
and falls back to access token, so either works.

Now check **Scopes**: `groups` should already exist as a default
custom scope. If not, add it under **Security → API → Authorization
Servers → default → Scopes**.

---

## 6. Refresh token rotation

**Applications → Periscope → General → General Settings → Edit**:

- **Refresh token behavior**: **Rotate token after every use**
- **Grace period for token rotation**: 30 seconds
- **Refresh token expiration**: **Expires after** 28800 seconds (8 h)
  — match `auth.yaml: session.absoluteTimeout`.
- (Idle is enforced server-side by Periscope, not by Okta.)

Save.

---

## 7. Map fields → `auth.yaml`

Open `examples/config/auth.yaml.okta`, replace the placeholders:

| Placeholder | Source |
|---|---|
| `<your-org>` in `oidc.issuer` | Your Okta org subdomain — e.g. `acme` for `acme.okta.com` |
| `<auth-server>` in `oidc.issuer` | `default` for the default Authorization Server, otherwise its name |
| `<your-application-client-id>` | Application **General → Client Credentials → Client ID** |
| `<your-host>` in `redirectURL` and `postLogoutRedirect` | `localhost:5173` for dev or your prod host |
| `authorization.allowedGroups` | The Okta group names you assigned in 4 |

Then run Periscope:

```sh
export OIDC_CLIENT_SECRET="<the secret from Okta General → Client Credentials>"
export PERISCOPE_AUTH_FILE="$(pwd)/examples/config/auth.yaml.okta"
go run ./cmd/periscope
```

(Or use the Helm chart with `auth.yaml.okta` content pasted into your
values — see `docs/setup/deploy.md`.)

---

## 8. Verify

1. Open `http://localhost:5173/`. You should land on Periscope's
   `<LoginScreen>` with a "sign in with okta" button.
2. Click it. You're 302'd through `/api/auth/login` to Okta's
   `/oauth2/<server>/v1/authorize`.
3. Sign in (Okta will MFA you if your org enforces it). Okta sends
   you back to `/api/auth/callback`.
4. Periscope verifies the ID token, applies the `allowedGroups`
   gate, sets the session cookie, 302s you to `/`.
5. The dashboard loads. Click your avatar in the cluster rail —
   the popover shows your email, an `oidc` badge (was `dev` in dev
   mode), and your groups.

If you get **403 "your account is not in any group that has Periscope
access"** — re-check 4 (user is in `periscope-users`, group is
assigned to the Application) and 5 (the `groups` claim filter is
permissive enough).

---

## Common pitfalls

- **`groups` claim is empty in the ID token.** Either the claim isn't
  configured (5) or the user isn't a member of any group that
  matches the claim filter. Fix by widening the filter to `.*` while
  debugging, then tighten it once it works.
- **`Sign-in redirect URI mismatch`.** Okta enforces exact-string
  match. Periscope's callback is exactly `/api/auth/callback`. No
  trailing slash, no extra query params.
- **Refresh fails after a few minutes.** Your auth server's access
  token lifetime is set very short (Okta defaults vary). Bump it on
  the Authorization Server's **Access Policies → default → Edit
  Rule** so refreshes don't churn.
- **Login bounces to Okta forever.** Check `oidc.issuer` has no
  trailing slash for `/oauth2/default` — Okta's `iss` claim is
  exactly `https://acme.okta.com/oauth2/default` without trailing
  slash.
- **Org-level Authorization Server vs default.** If you're on an Okta
  Enterprise org and your security team manages a custom AS, point
  `oidc.issuer` at *that* one. The "default" AS is a fallback
  Periscope works with happily on any org.
