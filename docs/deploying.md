# Deploying the suite

One Helm release installs Stator, Armature and Exciter at the versions in
`versions.yaml`, with a Keycloak holding the realm `dynamo`, one S3 store and
one Kroki for diagrams. After every install and upgrade, the connect Job
wires the three to each other. `helm test` then checks from inside the
cluster that they are connected.

The tools' charts 0.2.0 cannot be pointed at that Kroki yet
(praetorianer777/Stator#558, Cloudster1/Exciter#451), and their own Krokis
stay off so the suite runs one: until then Stator draws Mermaid diagrams
alone and Exciter shows diagrams as their source. The compose stack draws
them all.

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
   account in once, checks that it is then signed in to all three, and asks
   Stator and Exciter as that account whether they reach their partners on
   its behalf.

## People connected at sign-in

The chart switches the token exchange on in Stator and Exciter
(`stator.armature.tokenExchange`, `stator.exciter.tokenExchange`,
`exciter.armature.tokenExchange`, `exciter.stator.tokenExchange`), and the
connect Job registers each of them as a partner app in the others. A person
who signs in to Stator or Exciter is then connected to the partners as
themselves, as long as they are a member there (`members`). Set a switch to
`false` to have people paste a token on their profile instead.

A release installed before the switches were on gets them with a plain
`helm upgrade` to this chart version; values of your own that set them to
`false` keep them off.

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
| `oidc.existingSecret` | `stator-client-secret`, `armature-client-secret`, `exciter-client-secret`, and with the bundled Keycloak `rotor-client-secret` and `rotor-control-plane-client-secret` | the OIDC clients' secrets; the bundled Keycloak imports them from its environment, Rotor's two among them, which its realm holds before Rotor joins the chart |
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
the connect Job again, which changes only what is missing. A client secret
changed in `oidc.clients` or in `oidc.existingSecret` reaches the tools on
that run: the Job remembers what it sent them in the volume
`<release>-connect` (`connect.storage`), since a tool never answers which
secret it holds. Run `helm test` again afterwards. `make test-chart-upgrade`
tests this upgrade from the previous combination (README, "Testing an
upgrade").

## Before you install: the same install in kind

`DYNAMO_FROM_RELEASE=1 make test-chart` does the same in a fresh local kind
cluster. It pulls the published chart and every image from its registry, with
nothing from this machine, then installs, runs `helm test` and upgrades.
