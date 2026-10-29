# Look and feel

The three web apps come from one kit: the same tokens, fonts, colours, radii
and theme engine, with Stator's kit ported from Armature's. What they share
is not repeated here. This page settles where they drifted apart, and names
for each point the version the three follow. The sign-in page, the settings,
the Admin page, the settings of a project or space and access are in
[administration.md](administration.md).

## Feedback

- **Toasts:** what happens after an action, or what failed, is a toast
  (Armature's `Toast` and `MutationErrorToasts`, the rules of its
  `docs/design/06-feedback-rules.md`). An edit you watch happen may say
  "Saved." in place.
- **Confirmation:** a destructive action asks in a `ConfirmDialog` that names
  what goes with it. `window.confirm` is not used: it ignores the theme and
  cannot word a consequence.
- **Dialogs** follow Armature's API: `open`; `size` sm, md or lg; a
  `description`; a pinned `footer` that keeps the buttons in view; and
  `Drawer` and `DockedPanel` beside it.
- **Errors inside the shell:** a page that is not there, or fails, stays inside
  the shell with its sidebar: a page header, an empty state and the way home
  (Stator's and Exciter's `routes/root.tsx`).
- **Loading:** a skeleton of what is coming, never the word "Loading".

## The shell

- **The rail:** 48 px. The same icon stands for the same place in every tool:
  projects are a folder.
- **The narrow strip:** 48 px, holding the menu button, the installation's
  name and the bell.
- **The sidebar:** 232 px, a drawer below 768 px. Its head is the tool's mark
  and the installation's name. The build stamp is the shared `BuildStamp`.
- **The account menu:**
  - a round button that opens to the right of the rail;
  - a header with the avatar beside the name, address and role;
  - separators between its groups;
  - "Switch to" with each tool's real logo in one format;
  - signing out goes to the sign-in page however it ends.
- **The palette:** Ctrl+K, and Cmd+K on a Mac, with the hint translated. It
  finds:
  - the tool's own things, such as issues, pages or requirements;
  - projects or spaces;
  - places and actions;
  - recent pages.

  It shows them in groups, each read out as a group.
- **Breadcrumbs** are derived from the places, the sidebar's path minus the
  current page, and drawn in the sticky strip (Exciter's `Crumbs`). Pages do
  not pass their own.
- **Badges:** counts use one component, the accent-subtle style capped at 99+
  (Exciter's `Count`).
- **Notifications:** the bell, a page of its own named "Notifications", and a
  count in the sidebar.
- **Page widths** come from `Page`: narrow 64rem, content 100rem, wide the
  whole width (Armature's). Pages do not type their own widths.

## The kit

- **The base** is Armature's kit, which has the most. It adds Exciter's `Radio`
  and `WordButton`, and Stator's `ref` on `Button` and `Field`.
- **Component behaviour:**
  - Tabs scroll sideways on a phone.
  - A chip truncates a long name and shows it in full on hover.
  - An error banner can bring itself into view.
- **Menus** take groups as Stator's `MenuGroup`, read out as groups, plus
  Exciter's `checked` and Armature's `separated`. A menu is drawn inside its
  trigger's landmark.
- **Icons** are one set of 16 px glyphs with a 1.5 stroke. A glyph of a name is
  drawn the same in every tool.
- **Charts** use one palette, the six `--color-chart-*` tokens.

## Time, language and themes

- **Dates and times** are written in the person's language and their time
  zone from the profile, with the browser's as the default. Relative times,
  such as "2 hours ago", are translated.
- **Language and time zone** are chosen on the profile, saved with the
  profile's Save.
- **Strings** live in per-feature catalogues, in English and German. No string
  a person reads is hard-coded, theme token labels included.
- **The default theme** is the installation's and is called "everyone's".
  Administrators set it where the themes are, not on a page of its own. Each
  tool can follow a partner's theme.

## Accessibility

The same in all three:
- the skip link and the focus ring;
- the landmarks, the rail's top a `div` inside the navigation;
- menus inside their landmark.

## Where this is going

Much of the kit and shell is already identical in two or all three tools:
- Card, Table, Tag, EmptyState, Skeleton, Segmented and the theme loader in
  all three;
- Dialog, Tabs, Chip, ErrorBanner, the formats and the i18n runtime in Stator
  and Exciter;
- the rail, the drawer and the theme code, nearly so.

Once the tools follow this page, those parts can become one shared package,
with the product's name and storage prefix as parameters, so they cannot
drift again.
