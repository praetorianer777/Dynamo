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

### Names

- A name is lowercase words joined by hyphens, then `@` and an integer:
  `page-remote-links@1`. A dot groups names of one area, as in
  `oauth.partner-tokens@1` and `webhooks.partner@1`. No product names.
- One name means one promise, whoever offers it. `webhooks@1` from Stator
  promises the same routes, envelope and signing as `webhooks@1` from
  Armature; a feature that only one tool has is named after what it works on
  (`page-remote-links@1`, not a second `remote-links@1` on Stator).
- Tools report only names from the table below. A new name is added here, by
  pull request, before any tool reports it, and the tool's own list of
  promises (`capabilities.go`) says the same as the row here.
- A breaking change to what a capability promises gets a new number
  (`webhooks@2`). The tool reports the old one alongside for at least one
  suite release, as long as it still honours it.

Paths are below `/api/v1`. Every call is made with a session or a personal
access token of the person it acts for; no capability needs a shared service
account.

### Offered by Armature

| Capability | Promises | Relied on by |
|---|---|---|
| `issue-embed@1` | An issue read by its key, and issues found by NQL or by the words typed so far: `GET /issues/{issueKey}`, `GET /issues`, `GET /issues/suggest`. | Stator, Exciter |
| `remote-links@1` | The pages elsewhere that are about an issue: listed, put on by url, retitled by putting the same url again, taken off (`GET` and `POST /issues/{issueKey}/remote-links`, `DELETE /issues/{issueKey}/remote-links/{remoteLinkID}`), and announced as `issue.remote_link.added` and `issue.remote_link.removed`. | Stator, Exciter |
| `remote-link-relations@1` | A remote link says whether it is a page or a requirement, on the link, on the request that puts it and in both topics; one sent without it is a page. | Exciter |
| `reports@1` | The report kinds a project has, one report computed now, and a dashboard shared by a link read without an account: `GET /projects/{projectKey}/report-kinds`, `GET /projects/{projectKey}/reports/{kind}`, `GET /shared/{token}`, `GET /shared/{token}/widgets/{widgetID}`. | Stator |
| `plan@1` | A project laid out against a calendar, its people's hours against the work, and the share of each person's week, read and set: `GET /projects/{projectKey}/plan`, `/resources`, `/allocations`, `PUT /projects/{projectKey}/allocations/{userID}`. | Stator |
| `calendar@1` | Everything a project has dated in a month: issues, sprints, milestones, versions, holidays and who is away: `GET /projects/{projectKey}/calendar`. | Stator |

### Offered by Stator

| Capability | Promises | Relied on by |
|---|---|---|
| `page-links@1` | Each Armature issue a published page names as a chip or an issue block carries the page's remote link, put on and taken off as the page changes, and the page says how each stands: `GET /pages/{pageID}/armature-links`. | none yet; Stator's own links on Armature issues |
| `page-remote-links@1` | Another tool puts links to its objects on a page as somebody who may edit it, a link with the same url replacing the one there (`GET` and `POST /pages/{pageID}/remote-links`, `DELETE /pages/{pageID}/remote-links/{linkID}`), and hears through webhooks when somebody takes one off. | Exciter |
| `webhook-receiver@1` | Armature's signed deliveries of envelope version 1 received at `POST /armature/webhook` for the issue topics, so pages show a change to an issue at once. | Dynamo, which registers the endpoint in Armature |

### Offered by Exciter

| Capability | Promises | Relied on by |
|---|---|---|
| `requirement-embed@1` | A requirement read by its key, and requirements found by their words or the first letters of their titles: `GET /requirements/{reqKey}`, `GET /search`, `GET /search/quick`. | none yet |
| `requirement-list@1` | A project's requirements narrowed and ordered by a query, a window at a time, and what may come next at the caret of such a query: `GET /projects/{projectKey}/requirements`, `GET /projects/{projectKey}/requirements/suggest`. | none yet |
| `trace-links@1` | The trace links between requirements: listed from either end, made with a link type, and taken off: `GET /link-types`, `GET` and `POST /requirements/{reqKey}/links`, `DELETE /links/{linkID}`. | none yet |
| `specifications@1` | The projects a caller may read, and each one's requirements as the outline of its specification: `GET /projects`, `GET /projects/{projectKey}`, `GET /projects/{projectKey}/tree`. | none yet |
| `mcp@1` | An assistant works with requirements over MCP 2025-11-25 or 2025-06-18 as the person whose token it holds: `POST /mcp`. | assistants |

### Offered by more than one tool

| Capability | Offered by | Promises | Relied on by |
|---|---|---|---|
| `webhooks@1` | Armature, Stator, Exciter | Endpoints subscribed to topics, their deliveries, a redelivery, a test delivery and a new secret (`GET`, `POST /webhooks`, `PATCH`, `DELETE /webhooks/{id}`, `GET /webhooks/{id}/deliveries`, `POST /webhooks/{id}/deliveries/{deliveryID}/redeliver`, `POST /webhooks/{id}/test`, `POST /webhooks/{id}/rotate-secret`), each delivery the versioned envelope above, signed as `X-<Product>-Signature-256: sha256=<hex HMAC-SHA256 of the body>` with `X-<Product>-Event`, `X-<Product>-Delivery`, `X-<Product>-Timestamp` and `X-<Product>-Signature-Timestamped-256` over `{timestamp}.{body}`, and retried when it fails. | Stator, Exciter, Dynamo |
| `webhooks@2` | Exciter | What `webhooks@1` promises, and an endpoint registered for envelope version 2 is posted a CloudEvent 1.0 in structured mode whose data is version 1's payload. | receivers that speak CloudEvents |
| `theme-export@1` | Armature, Stator, Exciter | A theme exported as one `armature-theme/1` file with its pictures and fonts, and such a file imported as a theme of the caller's own: `GET /themes/{themeID}/export`, `POST /themes/import`. | Stator, Exciter (of Armature) |

### Partner access

How a partner acts for a person without the person pasting a token. A tool
that offers one of these is an OAuth 2.0 authorization server for its
partners, with discovery at `/.well-known/oauth-authorization-server` and
partner apps registered by an administrator.

| Capability | Offered by | Promises | Relied on by |
|---|---|---|---|
| `oauth.partner-tokens@1` | Armature | Authorization code flow with PKCE, refresh tokens and revocation, so a partner gets a person's token in one click with their consent; the token never exceeds the person's rights and shows in their list as a connected app. | Stator |
| `oauth.token-exchange@1` | Armature, Stator, Exciter (planned: Cloudster1/Armature#302, praetorianer777/Stator#461, Cloudster1/Exciter#273) | `POST /api/v1/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` (RFC 8693): `subject_token` is an access token of the shared Keycloak realm whose `azp` is the partner app's Keycloak client id and whose `aud` holds the offering tool's client; the app authenticates with its own credentials; the answer is an access and a refresh token within the person's rights, with no consent page. A partner uses it at the person's sign-in, so nobody connects by hand. | Stator, Exciter (planned: praetorianer777/Stator#430, #462, Cloudster1/Exciter#274) |
| `webhooks.partner@1` | Armature | A partner creates, rotates and changes its own webhook endpoint once an administrator agrees, and never touches others. | Stator |

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

All three answer `GET /api/v1/meta` with the capabilities above, send the
versioned envelope, use the `dynamo` realm and release by tag to GHCR with a
chart and a dispatch to Dynamo. None has tagged a release yet. What is left
for these rules:

- Exciter checks Stator for `page-remote-links` under any version; it should
  check `page-remote-links@1`.
- Nobody connects people across tools at sign-in yet; the issues are listed
  with `oauth.token-exchange@1` above.
