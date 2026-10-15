# Compatibility rules

Stator, Armature and Exciter release at their own pace. Dynamo pins one version
of each in `versions.yaml` and releases a combination only when the tests
across all three pass. The rules below are what lets a tool release on its own
without breaking the other two; each tool follows them in its API, its webhooks
and its release.

## APIs and webhooks change additively

- A new endpoint, a new optional request field, a new response field, a new
  webhook topic or a new payload field is always allowed.
- Receivers ignore fields and topics they do not know. A client that fails on
  an unknown field is the client's bug.
- Removing or renaming anything a partner can see (an endpoint, a field, a
  topic, a header, an error code, the meaning of a value) is a breaking change.
  It is deprecated first: the old form keeps working and is marked deprecated
  in the OpenAPI document and the CHANGELOG, for at least one suite release
  after the one that introduced the replacement. Only then is it removed.
- A new required request field is a removal of the request that did not carry
  it, and follows the same path.

## Versions that only rise for breaking changes

- `apiVersion` is `info.version` of the tool's OpenAPI document, an integer as
  a string (`"1"`). It rises only when a deprecated part is finally removed or
  something else breaks clients. Additive changes leave it alone.
- The webhook envelope carries an integer `version` and the same number in the
  header `X-<Product>-Webhook-Version` (`X-Armature-Webhook-Version`):

  ```
  { "version": 1, "id": "...", "topic": "issue.updated", "orgId": "...", "occurredAt": "...", "payload": { ... } }
  ```

  It rises only for a change that breaks receivers. A sender of version N+1
  keeps sending N to endpoints registered for N for at least one suite
  release. A receiver refuses a version it does not know with a clear status
  and log line instead of misreading it. An envelope without `version` is
  version 0, the format before the field existed, and has the same fields.
- The tool's own version (`X.Y.Z` in `VERSION`) follows semver and says
  nothing a partner may rely on: partners decide from `apiVersion` and
  capabilities, never from the tool's version number.

## What every tool reports

Each tool answers the same unauthenticated endpoint:

```
GET /api/v1/meta
{
  "product": "stator" | "armature" | "exciter",
  "build": { "version": "1.3.0", "commit": "abc1234", "builtAt": "2026-10-14T09:00:00Z" },
  "apiVersion": "1",
  "capabilities": ["remote-links@1", "webhooks@1"]
}
```

`build.version` is `X.Y.Z` for a build from a release tag and
`X.Y.Z+N.g<hash>` (or `+dev`) for anything else, so a development image is
never taken for a release. Dynamo checks that each tool in the stack reports
the version pinned in `versions.yaml`.

## Capabilities

A capability names one integration feature a partner relies on, with a version
after an `@`. A caller decides from capabilities, not from version numbers,
whether a feature is there, and says in a sentence which partner to update
when one it needs is missing.

The name says what is offered, not who offers it: the product answering `GET
/api/v1/meta` is the one that offers it. `remote-links@1` from Armature means
remote links on issues, from Stator remote links on pages.

Tools report only names from this table. A new name is added here, by pull
request, before any tool reports it. A breaking change to what a capability
promises gets a new number (`remote-links@2`); the tool reports the old one
alongside for at least one suite release, as long as it still honours it.

| Capability | Offered by | Promises | Relied on by |
|---|---|---|---|
| `remote-links@1` | Armature | `GET`, `POST` and `DELETE /api/v1/issues/{key}/remote-links[/{id}]`. `POST` takes `{url, title, source, iconUrl?}` and upserts by `url` (201 created, 200 retitled); a link answers `id, url, title, source, iconUrl, createdBy, createdAt, updatedAt`. Links show on the issue. | Stator, Exciter |
| `remote-links@1` | Stator | The same three routes and shapes on `/api/v1/pages/{pageID}/remote-links`; links show on the page. Not built yet (Cloudster1/Exciter#42 asks for it). | Exciter |
| `webhooks@1` | Armature | Endpoints registered per organization through `/api/v1/webhooks` with a URL and topics; deliveries are `POST` with the versioned envelope above, signed as `X-Armature-Signature-256: sha256=<hex HMAC-SHA256 of the body>`, with `X-Armature-Event`, `X-Armature-Delivery`, `X-Armature-Timestamp` and `X-Armature-Signature-Timestamped-256` over `{timestamp}.{body}`; secrets start with `armature_whs_`; failed deliveries are retried. Topics include `issue.created`, `issue.updated`, `issue.transitioned`, `issue.deleted`, `comment.added`, `issue.remote_link.added` and `issue.remote_link.removed`. | Stator, Exciter |
| `issue-embed@1` | Armature | `GET /api/v1/issues/{key}`, `GET /api/v1/issues` with a query, `GET /api/v1/projects` and the issue types, readable with a member's personal access token, enough to show an issue's key, summary, status, type and assignee inside another tool; the browser link is `<base>/issues/{key}`. | Stator, Exciter |
| `project-views@1` | Armature | `GET /api/v1/projects/{p}/calendar`, `/reports/...` and `/plan`, for the calendar, chart and roadmap a page shows. | Stator |
| `theme-export@1` | Armature | `GET /api/v1/themes/active` and `GET /api/v1/themes/{id}/export` in a format the other tools can apply. | Stator, Exciter |
| `page-links@1` | Stator | Pages can be found and read with a member's token (`GET /api/v1/pages/{id}`, a search by title), enough to show a page's title, space and link inside another tool. | Exciter |
| `trace-links@1` | Exciter | A requirement's trace links to Armature issues and Stator pages can be read with a member's token, enough to show which requirements an issue or page traces to. | Stator, Armature |

Every tool's personal access tokens are sent as `Authorization: Bearer
<token>`; capabilities never require a shared service account.

## Upgrade order

- Within one suite release, the three tools upgrade in any order. A suite
  release only pins versions that were tested together, and each of them
  keeps working with the partners' versions of the previous suite release.
- Across suite releases, upgrade Armature first, then Stator and Exciter.
  Stator and Exciter call Armature and receive its webhooks; Armature does not
  depend on them, so a newer Armature can serve older callers while the
  deprecation window lasts, but a newer caller may need what only a newer
  Armature offers.
- Skipping a suite release is not supported: removals wait one suite release,
  so going from N to N+2 can lose something N+1 deprecated.

## Contract tests run against main every night

- Each tool keeps contract tests against the OpenAPI documents of the
  partners it calls, at the versions pinned in Dynamo's `versions.yaml`, and
  against the webhook envelope it receives. They run on every pull request.
- The same tests also run nightly against each partner's `main`, so a change
  that breaks a partner shows up before it is tagged, while it can still be
  reverted or deprecated properly.
- Dynamo's nightly run starts the newest code of all three together as an
  early warning; its tests on a release dispatch are what decide whether a
  combination is released.

## Where each tool stands

What is still missing for these rules, tracked in the tools:

- Stator: `GET /api/v1/meta` is there with an empty capability list
  (praetorianer777/Stator#409); no release tagged yet.
- Exciter: `GET /api/v1/meta` lacks `apiVersion` and `capabilities`
  (Cloudster1/Exciter#99, #100); release by tag and the chart are open (#101,
  #104); it checks Stator for the name `page.remote-links`, which becomes
  `remote-links@1` under this table.
- Armature: no `GET /api/v1/meta`, no versioned webhook envelope, no published
  images or chart and no release yet.
