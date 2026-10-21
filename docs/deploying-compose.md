# Running the suite on a server with Docker Compose

`deploy/docker-compose.yml` runs Stator, Armature and Exciter at the versions
in `versions.yaml` on one machine, with a Keycloak holding the realm `dynamo`,
one object store and Mailpit. The same file runs the development stack
behind `make up`. On a server its settings come from `deploy/.env`, and
`docker compose up` alone starts the suite and connects the three to each
other. For Kubernetes, `docs/deploying.md` does the same with the chart.

## What the server needs

- Docker with Compose v2.24 or newer, and a clone of this repository.
- A DNS name for each tool and one for Keycloak, all pointing at the server.
- Ports 80 and 443 open to the internet, so Caddy can get certificates from
  Let's Encrypt. With a proxy of your own, see below.
- Room for about 30 containers: each tool brings its own PostgreSQL, Valkey
  and render service.

## First start

1. In `deploy/`, make the settings:

   ```sh
   cd deploy
   ./compose-secrets.sh
   ```

   It copies `.env.example` to `.env`, and gives every password, client
   secret, key and S3 secret left empty there a random value. It prints the
   sign-in passwords it made, for Keycloak's administrator and each tool's,
   and says so only this once; they stay in `.env`, which only you may read.
2. Set in `.env` what only you know. The script warns about what is still
   the example's:
   - `STATOR_URL`, `ARMATURE_URL`, `EXCITER_URL` and `KEYCLOAK_URL`, the
     addresses people open;
   - each tool's administrator, `*_ADMIN_EMAIL`, and in `DYNAMO_MEMBERS`
     who of the realm is let into all three before their first sign-in;
   - mail: `SMTP_ADDR` and the senders. Stator and Exciter send without a
     login, so give them a relay that lets them. Only Armature logs in, with
     `SMTP_USER` and `SMTP_PASSWORD`. `mailpit:1025`, the default, keeps every
     mail in the bundled Mailpit.
   - `CADDY_GLOBAL_OPTIONS`, such as `email admin@example.com`, for Let's
     Encrypt's notices.
3. Run the script again, so the sign-in settings follow from the addresses,
   and start:

   ```sh
   ./compose-secrets.sh && docker compose up -d
   ```

   The tools migrate their databases first. The connect service then waits
   until all three answer, connects them and exits. It takes a few minutes
   on a first start. `docker compose logs connect` says what it did.

The suite then runs at your addresses. Sign in to Keycloak at
`KEYCLOAK_URL/admin` as `KEYCLOAK_ADMIN` and add the people of the realm
`dynamo`. Every tool's sign-in page then offers single sign-on. A value you
set in `.env` is never changed by the script, and a value it made never
changes on later runs either.

## What runs

`COMPOSE_PROFILES` in `.env` chooses what runs besides the three tools, the
object store and Mailpit:

| Profile | What it adds |
| --- | --- |
| `keycloak-server` | Keycloak with its own PostgreSQL. It imports `keycloak/realm.server.json` on its first start: the realm without demo users, each client's secret from `.env`, each tool's address as its only redirect. Its accounts survive restarts. |
| `connect` | dynamo-connect, on every `docker compose up`: it changes only what differs, so it is safe to run again. |
| `caddy` | HTTPS for the four addresses, with certificates from Let's Encrypt (`caddy/Caddyfile`). |
| `sample-data` | Imports `DYNAMO_SAMPLE_SOURCES` after connect (the README's "Sample data"). It needs a dynamo-connect newer than 0.1.0. |

**A Keycloak of your own.** Leave out `keycloak-server`, set `OIDC_ISSUER` to
your realm and the three clients' IDs and secrets, and leave `KEYCLOAK_URL`
empty. `docs/keycloak.md` says what the realm must hold.

**A proxy of your own.** Leave out `caddy` and send each address to its port
on `127.0.0.1`: `STATOR_WEB_PORT`, `ARMATURE_WEB_PORT`, `EXCITER_WEB_PORT`
and `KEYCLOAK_PORT`. Set `X-Forwarded-Proto` and `X-Forwarded-Host`. The
tools run in production mode with secure cookies, so the proxy must serve
HTTPS.

## Upgrade

Pull the repository, then set the new versions in `.env` from
`versions.yaml`: `STATOR_VERSION`, `ARMATURE_VERSION`, `EXCITER_VERSION` and
`CONNECT_IMAGE`. Then:

```sh
./compose-secrets.sh && docker compose up -d
```

The script adds any secret a newer `.env.example` asks for. Compose recreates
what changed, the tools migrate, and connect runs again.

## Backup

Everything the suite keeps is in Compose's volumes: each tool's
`*-postgres` and `*-valkey`, `keycloak-postgres`, `seaweedfs-data` with the
files, and `caddy-data` with the certificates. Keep `.env` with them: the
databases are useless without its passwords, and each tool's `*_SECRET_KEY`
seals secrets inside its database.

## Before you start: the same in a test

`make test-server` starts the suite the way the steps above do, from
`.env.example`:

- under names only the test resolves, with Caddy's own certificate
  authority instead of Let's Encrypt;
- in production mode;
- with a person it adds to Keycloak.

It then signs that person in through HTTPS, checks every webhook and the
sample data, runs connect again expecting no change, and restarts Keycloak to
check that the person can still sign in.
