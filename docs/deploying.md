# Deploying the suite

One Helm release installs Stator, Armature and Exciter at the versions in
`versions.yaml`, with a Keycloak holding the realm `dynamo` and one S3 store.
After every install and upgrade, the connect Job wires the three to each
other. `helm test` then checks from inside the cluster that they are
connected.

On one machine without Kubernetes, `docs/deploying-compose.md` does the same
with Docker Compose.

## What the cluster needs

- An ingress controller, and DNS names for Keycloak and the three tools.
- TLS certificates for those names: cert-manager with an issuer, or Secrets
  you create under the names the values give.
- A default StorageClass: each tool's PostgreSQL, Keycloak and the object
  store keep volumes.
- Pulls from `ghcr.io`: the images and charts are public.

## Install

1. Copy `deploy/charts/dynamo/values-cluster.yaml`, replace every
   `example.com` address, and replace every `CHANGE-ME` with a secret of your
   own. The object store's keys must be the same in `objectStore` and in each
   tool's `secrets`, and the chart refuses keys that differ.
2. Install the released chart:

   ```sh
   helm install dynamo oci://ghcr.io/praetorianer777/charts/dynamo \
     --version <version> -n dynamo --create-namespace -f values.yaml --timeout 30m
   ```

   Don't add `--wait`: the tools' own hooks run first, and the connect Job
   runs last of all. A finished install is a connected suite.
3. Check it:

   ```sh
   helm test dynamo -n dynamo --logs
   ```

   The test Pod has each tool send a test delivery on every webhook the
   connect Job made. When `check.signIn` names an account, it also signs that
   account in once and checks that it is then signed in to all three.

Each tool's administrator is kept in a Secret: `stator-bootstrap-admin`,
`exciter-bootstrap-admin` and `dynamo-armature-admin`. Keycloak's admin is
kept in `dynamo-keycloak-admin`.

## Secrets made beforehand

Every secret can come from a Secret that exists before the install instead of
a value, so a values file holds none, and External Secrets, Sealed Secrets, a
vault or `kubectl` can make them. `scripts/create-secrets.sh` asks for each
one, or generates it, and makes them; with `--print` it writes them as YAML
instead, and `--own-s3` makes one key pair per tool for an S3 of your own.

| Value | Secret's keys | Holds |
| --- | --- | --- |
| `oidc.existingSecret` | `stator-client-secret`, `armature-client-secret`, `exciter-client-secret` | the OIDC clients' secrets; the bundled Keycloak imports them from its environment |
| `keycloak.admin.existingSecret` | `username`, `password` | Keycloak's administrator |
| `armatureAdmin.existingSecret` | `username` (the email), `password` | Armature's owner |
| `objectStore.existingSecret` | `s3-access-key`, `s3-secret-key` | the bundled store's keys |
| `<tool>.secrets.existingSecret`, with `<tool>.secrets.create: false` | `s3-access-key`, `s3-secret-key` | a tool's S3 keys |

With the bundled store, each tool names the store's Secret, since the store
has one key pair; the chart refuses anything else. With an S3 of your own
(`objectStore.enabled: false`), each tool names its own.

## Sample data

With `sampleData.enabled`, a Job after the connect Job imports the projects
`sampleData.sources` names into all three, linked to each other; the
README's "Sample data" says what each source becomes. Sources other than
the built-in `sparkplug` are read from GitHub, which the Job must reach;
without a token GitHub allows 60 requests an hour, so for several or large
sources make a Secret with a token and name it in
`sampleData.github.tokenSecret`:

```sh
kubectl -n dynamo create secret generic github-token --from-literal=token=<token>
```

Where the cluster reaches the internet only through a proxy, name it:

```yaml
proxy:
  https: http://proxy.example.com:3128
  noProxy: .example.com     # more to reach directly; the suite's own names always are
```

The Job runs again on every upgrade and changes only what is missing.

## Upgrade

`helm upgrade` with a newer chart version runs the tools' migrations and then
the connect Job again, which changes only what is missing. Run `helm test`
again afterwards.

## Before you install: the same install in kind

`DYNAMO_FROM_RELEASE=1 make test-chart` does the same in a fresh local kind
cluster. It pulls the published chart and every image from its registry, with
nothing from this machine, then installs, runs `helm test` and upgrades.
