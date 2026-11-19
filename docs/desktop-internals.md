# Claude Desktop internals that Claude-Syncer relies on

Everything here was verified by experiment and by reading the app bundle of
**Claude Desktop 6.6395.1 for macOS** (November 2026). None of it is a public
interface; any app update may change it. Claude-Syncer validates the records
it reads and leaves alone anything that does not match what is described here.

## Where sessions live

| What | Where | Shared by accounts? |
|---|---|---|
| Terminal transcripts | `~/.claude/projects/<encoded cwd>/<cliSessionId>.jsonl` | Yes: one `~/.claude` for everyone |
| Desktop session records | `~/Library/Application Support/Claude/claude-code-sessions/<accountUuid>/<orgUuid>/local_<id>.json` | No: one folder per account and org |
| Signed-in account | `lastKnownAccountUuid` in `~/Library/Application Support/Claude/config.json` | |

A session folder holds:

- `local_<id>.json`: one record per session. Compact JSON, keys in insertion order.
- `deleted_<id>`: a tombstone. Its content is a millisecond timestamp.
- `archived-sessions.idx`: `{"v":1,"archived":["local_<id>",...]}`, exactly the records
  with `isArchived: true`, sorted. The app uses it to defer loading archived sessions.
- `scheduled-tasks.json`, `waiting-input/`: per-account data, never synced.

## Behaviours that shape the design

**The signed-in account is held in memory.** The app loads the signed-in
account's records at start-up and writes them back when you switch accounts.
Anything written into that account's folders while it is signed in can be
overwritten. Records **added** to it survive but are only shown after the app
restarts (or after switching away and back). Claude-Syncer therefore only
adds files there, and applies other changes after the switch.

**Symlinks do not work.** The app does not follow symlinks when saving, and
fails silently ("Failed to save session" in `~/Library/Logs/Claude/main.log`).
Records must be real copies.

**Several CLI ids per record.** A record claims transcripts through five
fields: `cliSessionId`, `unarchivedCliSessionId`, `preClearCliSessionId`,
`priorCliSessionIds`, `overflowCliSessionIds`. Different records can claim the
same transcript (forks, `/clear` chains), so a CLI id does **not** identify a
session. The record id (`local_<id>`) does, and copies in other accounts keep it.

**Record id ≠ CLI id.** `local_X` is often not the record's `cliSessionId`.

**Archiving rewrites a record without touching `lastActivityAt`.** File mtime,
not any timestamp inside the record, tells which copy changed last.

**`local_*.json.tmp` belongs to the app** (crash recovery). Our temporary
files are named `.claude-syncer-*.tmp`.

**SSH/WSL records** (`sshConfig`/`wslConfig`) are excluded from the app's
local transcript bookkeeping and from ours.

## Deleting a session in the app

Observed three times, and confirmed in the app's code:

1. The record file is removed from the signed-in account's folder.
2. Two tombstones are written there, with the same timestamp:
   `deleted_<record id>` and `deleted_<cliSessionId>`. They can arrive
   **several seconds after** the record is removed (seen on 2026-11-19 with a
   multi-select delete). When several sessions are deleted together, they
   share one timestamp. A record whose id equals its own `cliSessionId` gets
   one tombstone, and another record's delete may overwrite it.
3. The transcript is removed **only if no record in any other account or
   profile on disk claims it** (`cliSessionIdsClaimedOnDisk`), no running
   Claude Code process is writing it, and nothing else wrote to it since the
   app last used it. It then leaves `<id>.desktop-released.json`
   (`{"v":1,"releasedAt":...,"reason":"delete"}`) next to where it was.
   Otherwise the transcript is silently kept, to be removed by Claude Code's own
   `cleanupPeriodDays` clean-up.
4. Remote Control bridges for the session are released on the server.

Other accounts' folders are not touched. Once records are copied to every
account, step 3 never removes the transcript, because the other copies claim
it. Claude-Syncer does it instead, once it has removed every copy.

The app's transcript recovery (`CCD-SessionRecovery`) skips any CLI id that
has a tombstone in **any** account's folder.

## Records imported from the terminal

A minimal record is enough. The app loads it as is:

```json
{"sessionId":"local_<id>","cliSessionId":"<cli id>","cwd":"/path","originCwd":"/path",
 "createdAt":<ms>,"lastActivityAt":<ms>,"isArchived":false,"title":"…",
 "titleSource":"auto","permissionMode":"default",
 "adoptedFromOtherSurface":true,"fromCliTranscript":true}
```

`titleSource` is `"user"` for a title someone set and `"auto"` for a
generated one.

Transcripts that should **not** become records:
- entrypoint `claude-desktop`: the app wrote it, and the record is missing because it was deleted;
- entrypoint `sdk-*`: SDK or `claude -p` runs;
- no message typed by a person (only tool results, slash-command echoes, system text).

Title, in order of preference: the last `custom-title` (set by `/rename`), then
`ai-title` (or a legacy `summary`), then the first thing the user typed.

## Account-local record fields

These belong to the account or to the app's runtime and are never copied:
`remoteMcpServersConfig`, `enabledMcpTools`, `toolSurfaceSnapshot`,
`bridgeSessionIds`, `remoteControlAutoEligible`, `remoteControlUserEnabled`,
`steeredByRemoteClient`, `sendToProjectRemoteControlOffer`, `error`, `errorAt`,
`priorErrorMark`, `cuGrantFlags`, `cuAllowedApps`, `cuFlagsGrantedAt`,
`cuSelectedDisplayId`, `cuLastScreenshotDims`, `chromeTabGroupId`,
`alwaysAllowedReasons`, `sessionPermissionUpdates`, `scheduledTaskId`,
`publishedArtifacts`, `promptAppendSnapshot`, `pendingSystemReminder`.

`remoteMcpServersConfig` alone can be 250 KB per record. Copying it to every
account is what made earlier sync tools grow this directory twenty-fold.
