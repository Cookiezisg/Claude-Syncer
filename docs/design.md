# Design

## Goal

Every Claude Code session, whether started in the terminal or in Claude
Desktop, appears identically in every Claude Desktop account on the Mac:
created, renamed, archived or deleted anywhere, the same everywhere. The user
never has to think about it, and it costs next to nothing in disk or CPU.

## Model

A **session** is one desktop record id (`local_<id>`). Its **copies** are the
record files with that name in each `<account>/<org>` folder. Copies agree
on every *synced* field; account-local fields (see
[desktop-internals.md](desktop-internals.md)) stay as each account has them.

The terminal side needs no syncing: one `~/.claude` serves all accounts. It
feeds the model in three ways:

| Terminal event | Effect |
|---|---|
| New transcript, unclaimed by any record | imported as a new session (a minimal record in every folder) |
| `/rename` (a `custom-title` line) | the session's title |
| New activity in a terminal-born session | its `lastActivityAt` |

The terminal has no notion of archiving. A transcript disappearing is **not**
a deletion: Claude Code removes old transcripts by itself (`cleanupPeriodDays`).

## One pass

A pass is idempotent. launchd runs one whenever a watched path changes and
once a minute regardless (switching account does not always write anything
we can watch).

1. **Read** every folder (records, tombstones, archive index) and list every
   transcript. Transcript metadata is cached with a byte offset and only
   what was appended since is read.
2. **Group** copies by record id.
3. **Decide** each session's content (below), then fold in transcript events.
4. **Delete** sessions whose record id is tombstoned anywhere.
5. **Import** transcripts that nothing claims.
6. **Write**: in a *writable* folder, replace copies that differ; in an
   *add-only* folder (the signed-in account), only create missing ones. Keep
   `archived-sessions.idx` equal to the archived records.
7. **Record** what each copy now looks like.

## Deciding content: per-field three-way merge

The hard case: account A is signed in while the session is renamed in B. We
update every folder except A's. When you switch away from A, the app writes
A's copy back from memory, with the **old** title and a **new** mtime.
"Newest copy wins" would undo the rename.

So the state file remembers, for every copy, what it looked like when we last
saw it (its *base*), and for every session the content we agreed on. For each
field:

- copies whose value differs from their own base **changed** it; among them
  the most recently modified copy wins;
- if no copy changed it, the agreed value stays.

A's stale write-back equals its base and so does not vote. Edits to different
fields in different accounts both survive.

To keep the state small, a base is stored as the difference from the agreed
content. After a pass every writable copy is in sync, so its difference is
empty. The whole state file is under a megabyte for a few hundred sessions.

On the first sight of a session (no state yet), the most recently modified
copy wins.

## The signed-in account

Its folders are **add-only**: new records are created (shown after the app
restarts); updates and removals wait and are reported as *pending*. When the
signed-in account changes, both the old and the new account stay add-only for
60 seconds while the app finishes writing records back. After that the old
account is updated like any other.

If the signed-in account cannot be determined, every folder is add-only.

## Deletion

The app writes `deleted_<record id>` and `deleted_<cliSessionId>` when a
session is deleted. A record-id tombstone in any folder means the session is
deleted. A CLI-id tombstone alone does not, because another record may share
that transcript.

Carrying a deletion out:

1. Remove the copies from writable folders (into Claude-Syncer's trash).
2. Write tombstones for the record id and for every transcript **only this
   session** claims, in every folder (adding is allowed everywhere). This
   keeps both the app's recovery and our import from bringing it back.
3. Once no copy is left anywhere, move those transcripts to the trash, as
   the app does when only one account has the session. A transcript written
   in the last 10 minutes, or claimed by any other record, is never touched.

**Brake:** a pass that would delete more than 20 sessions deletes none,
notifies, and waits for `claude-syncer sync --force`.

## Safety

- Every write is atomic (temporary file and rename), and our temporary files can
  never be mistaken for the app's.
- Nothing is deleted outright: records and transcripts go to
  `~/Library/Application Support/claude-syncer/trash/<date>/`, purged after 7 days.
- A record that does not parse, is over 10 MB, or fails validation is left
  untouched, and the ids inside it still count as claims (as the app does).
  Transcripts are not removed while any record is unreadable.
- Account-local fields are never copied.
- Runs are serialised with a lock file.
- `claude-syncer sync --dry-run` shows every change without making it.

## Costs

- **CPU:** no daemon. A pass takes about a second on ~200 sessions × 14
  folders, at low priority, and only when something changed or once a minute.
- **Disk:** state under 1 MB, trash purged after 7 days, log capped at 2 MB.

## Known limits

- Sessions created elsewhere appear in the signed-in account only after the
  app restarts or you switch accounts. The app reads its folder only then.
- Remote Control bridges of a deleted session are released only in the
  account where it was deleted. That needs that account's sign-in.
- A rename in the app is not written back into the transcript, so
  `claude --resume` shows the terminal's last title.
- macOS only.
