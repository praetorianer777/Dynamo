# Signing in with a Keycloak of one's own

By default the stack starts its own Keycloak with the realm `dynamo` from
`deploy/keycloak/realm.json`, the canonical copy the three tools also import.
To sign in through a Keycloak that already runs, or through another realm,
set the issuer and the clients; the bundled Keycloak then stays off.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `OIDC_ISSUER` | the bundled realm, `http://localhost:<port>/realms/dynamo` | The realm's issuer as browsers reach it, exactly as its tokens name it. Setting it turns the bundled Keycloak off. |
| `OIDC_BACKCHANNEL` | `http://localhost:<port>=http://keycloak:8080` with the bundled one, else empty | `public-origin=reachable-origin` when the tools cannot reach the issuer at its public address, such as a Keycloak on the host or behind a proxy only browsers pass. |
| `OIDC_GROUPS_CLAIM` | `groups` | The claim that lists a person's groups. |
| `STATOR_OIDC_CLIENT_ID`, `STATOR_OIDC_CLIENT_SECRET` | `stator`, `stator-dev-secret` | Stator's client in the realm. |
| `ARMATURE_OIDC_CLIENT_ID`, `ARMATURE_OIDC_CLIENT_SECRET` | `armature`, `armature-dev-secret` | Armature's client. |
| `EXCITER_OIDC_CLIENT_ID`, `EXCITER_OIDC_CLIENT_SECRET` | `exciter`, `exciter-dev-secret` | Exciter's client. |
| `DYNAMO_MEMBERS` | `alice@suite.test=admin,bob@suite.test=member` | Who of the realm is let into all three before their first sign-in, as `email=admin` or `email=member`. |

For example:

```bash
make up OIDC_ISSUER=https://sso.example.com/realms/acme \
  STATOR_OIDC_CLIENT_SECRET=... ARMATURE_OIDC_CLIENT_SECRET=... EXCITER_OIDC_CLIENT_SECRET=... \
  DYNAMO_MEMBERS=ops@example.com=admin
```

`dynamo-connect` sets every tool's sign-in from these settings on each run,
so changing one and running `make connect` moves a running installation to
it. The tools seal the client secrets they store.

## What the realm must hold

The same as `deploy/keycloak/realm.json` does, which is the reference:

- One **confidential client per tool** with the standard flow on and a
  client secret.
  - Redirect URI: the tool's address followed by
    `/api/v1/auth/oidc/callback`, for example
    `https://wiki.example.com/api/v1/auth/oidc/callback`.
  - Web origin: the tool's address, or `+`.
  - PKCE S256 for `stator` and `exciter`. Armature's sign-in does not send a
    challenge yet, so its client must not require one.
- A **group membership mapper** on each client that puts the person's groups
  into `OIDC_GROUPS_CLAIM` of the ID token, the access token and userinfo,
  without the full path.
- Users with a **verified email**: each tool matches a person by email, and
  Armature refuses one whose email is not verified.

A Keycloak only the tools reach privately needs no extra setting: the tools'
outbound guards let the issuer's host through, as `mk/stack.mk` derives it
from `OIDC_ISSUER` or `OIDC_BACKCHANNEL`.

## Connecting people at sign-in

Once the tools connect people to each other at sign-in by token exchange
(see `oauth.token-exchange@1` in `docs/compatibility.md`), the realm also
needs an audience mapper on each client for the partners it calls:

- `stator`: audiences `armature` and `exciter`;
- `exciter`: audiences `armature` and `stator`.

Without them a sign-in still works; the partner simply refuses the exchange
and the person connects by hand.
