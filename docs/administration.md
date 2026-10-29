# Sign-in, settings and access

Stator, Armature and Exciter share one realm, so the same people sign in to
all three and the same administrators run them. This page is how the three
look and behave for them: the sign-in page, where settings live, and how
access is granted. Each tool follows it. Where a tool does not yet, the gap is
an issue in that tool, not an exception here.

For the sign-in page, Stator and Exciter are the reference. For settings and
access, Armature is. Where this page asks for more than the reference does, it
says so.

## The sign-in page

`/login`, the same in all three (Stator's and Exciter's `LoginPage`):

1. The logo, the tool's name, "Sign in" and "Sign in through your identity
   provider."
2. A card with the primary button "Sign in with SSO". Above it:
   - a waiting notice when the provider knew the person but nobody here let
     them in yet. It is a status, not an error: "Your request is waiting".
   - an error banner naming what went wrong and what to do, one sentence per
     reason: expired, not set up, no email, unverified email, switched off,
     refused, provider unreachable, anything else.
3. A second card for signing in with a password, its button secondary. Its
   heading and hint say who it is for in that tool. In Stator and Exciter, that
   is the local administrator. In Armature, it is people with a password
   there, such as portal customers, and the card keeps its link for a
   forgotten password.
4. Below the cards, only where the tool offers it, the way to sign up or to be
   invited.

Armature differs today: its password form comes first, the SSO button sits
under it, and a person waiting to be let in sees an error.

## Two areas: you, and the installation

| Area | Address | Who | Holds |
| --- | --- | --- | --- |
| Settings | `/settings` | everyone | Your profile with notifications, themes, access tokens and the apps you connected, and your accounts in the partner tools |
| Admin | `/admin` | administrators | everything that is the installation's |

- **The Settings hub** lists your pages. Each row has an icon, a label and a
  sentence that says what the page is for.
- **The Admin page** lists its pages in four sections, each row with the same
  sentence. On a wide screen a section nav sits beside it; on a narrow screen
  the nav is a menu.
- **Who sees Admin:** an administrator finds one "Admin" entry in the work
  sidebar, in the account menu and in the palette, where each page reads
  "Admin / <page>". Anybody else is refused once, for the whole of `/admin`:
  "Only administrators can open this page". Pages do not each refuse on their
  own.
- **Old addresses:** a page that moves from `/settings/<page>` to `/admin/<page>`
  redirects from its old address.

### The Admin sections

| Section | Armature | Stator | Exciter |
| --- | --- | --- | --- |
| People and access | Installation, Access, Users | Installation (with the brand), Access, Users | Installation (with the default theme), Access, Users |
| Work | Holidays, Workflows, Issue types, Project templates, Labels, Automation, Fields, Issue view | Hub, Templates, Stale pages, Example space | Link types |
| Integrations | Connected apps, Webhooks | Armature, Exciter, Connected apps, Webhooks | Armature, Stator, Connected apps, Webhooks |
| Records | Audit log | Audit log, Export and import | Audit log |

- **Connected apps** is the installation's apps, with each person's grants and
  tokens. It takes in Exciter's "all tokens" and "all connected apps".
- **Users** holds the join requests and switching an account off or back on.

## Access

`/admin/access` has the tabs **Roles**, **Matrix**, **Members**, **Groups** and
**Single sign-on**. Four ideas make it up:

| Idea | Answers | What it is |
| --- | --- | --- |
| Role | what | A named set of permissions. Each tool starts with built-in roles and administrators may add their own. A role lists its permissions outright, with no inheritance. Built-in roles cannot be deleted. |
| Matrix | what each role means | Every permission the tool checks, against every role, with a box at each crossing that saves when ticked. The global administrator's right to administer stays ticked. |
| Group | who | A named set of people. Its members are added by hand, or come from the identity provider through the group's provider claim, which is the name in the token's `groups` claim. Provider memberships are replaced at every sign-in, so somebody removed in Keycloak loses what the group gave them. |
| Grant | who holds what, where | A role, a scope and a subject. The scope is the whole installation or one project or space; the subject is a person or a group. |

- **What somebody may do** in a project or space is the union of every grant
  that reaches them: their own or their groups', and for that project or space
  or for the whole installation.
- **Everyone:** a built-in group holds every member, so a grant to "everyone"
  is a grant to that group. Armature asks for more here: it has no such group
  yet.
- **Buttons follow permissions:** the web app decides what to show by
  permission, never by a role's name, so a renamed or new role works
  everywhere at once.
- **Built-in roles per tool**, which existing grants move to when a tool takes
  on this model, so nobody's access changes:
  - Armature: global administrator, project administrator, scrum master,
    user, reader.
  - Stator: administrator over the whole installation, and in a space viewer,
    commenter, editor and space administrator, from today's permissions view,
    add comments, add pages and delete, and administer.
  - Exciter: administrator over the whole installation, and in a project
    viewer, reviewer, editor and project administrator, from today's project
    roles.

### Members and seats

A person's standing in the installation (member, or guest where the tool has
guests) says whether they are in and whether they hold a seat. What they may
do comes from grants only. The installation keeps at least one active
administrator: removing the last administrator grant is refused.

### Single sign-on

The tab shows:
- **Two-step sign-in.**
- **The provider:** issuer, client ID, client secret ("Stored. Leave blank to
  keep it."), groups claim, and **"Let people in by their groups"**, off until
  an administrator turns it on.
- **The buttons:** "Set up single sign-on" or Save, a quiet "Turn it off", and
  then "People can sign in at <address>" or "Turned off. The address would be
  <address>".

With "Let people in by their groups" on, somebody new whose provider group
holds a grant is let in with it, at sign-in and when a partner tool signs them
in through a token exchange. The audit log records "admitted by group <name>".
Anybody else waits under Users until an administrator lets them in. Somebody
switched off is refused either way.

Scopes, creating groups the provider names, and the callback address are not
on the tab. dynamo-connect sets sign-in up in each tool, and
[Keycloak](keycloak.md) says what the realm holds.

## Wording

The same words in all three:
- "installation", not organization or site;
- "people" and "members";
- "administrator", not admin, in sentences;
- "Let people in", "Turn it off", "Waiting to be let in".

A refusal is a sentence that says what to do about it.
