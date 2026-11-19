# Claude-Syncer

**Use several Claude accounts on one Mac as if they were one.**

Claude Desktop keeps a separate list of Claude Code sessions for every
account and organization you sign in with. Start a session under your work
account, switch to your personal one, and it is gone. Sessions started in the
terminal do not show up in Desktop at all.

Claude-Syncer keeps them all identical, quietly, in the background:

- A session started in **any account** shows up in **every account**.
- A session started in the **terminal** (`claude`) shows up in **every account**.
- **Rename, archive, delete** anywhere: the same everywhere. `/rename` in the terminal too.
- Switching accounts: everything is already there.

No daemon, no menu-bar icon, no notifications unless something needs you.

> Claude-Syncer is an independent project, not affiliated with Anthropic. It
> works with Claude Desktop's private on-disk format, which may change with
> any update. It is built to stop rather than guess when it sees something it
> does not recognize.

## Install

```sh
brew install cookiezisg/tap/claude-syncer
claude-syncer install
```

`install` runs a first sync, shows what it did, and starts the background
agent. You are done.

<details>
<summary>Without Homebrew</summary>

Download `claude-syncer-<version>-darwin-universal.tar.gz` from
[Releases](https://github.com/Cookiezisg/Claude-Syncer/releases), or with Go
1.25+:

```sh
go install github.com/Cookiezisg/Claude-Syncer/cmd/claude-syncer@latest
```

then run `claude-syncer install`. It copies itself to
`~/Library/Application Support/claude-syncer/bin/` so the background agent
keeps working wherever the download was. Run `claude-syncer install` again
after updating.
</details>

## Update

```sh
brew upgrade claude-syncer
```

Nothing else to do: the background agent runs the Homebrew link, so its next
pass uses the new version. When a new version reaches Homebrew you get one
notification. That check is a single anonymous request for the published
formula, at most once a day. Set `CLAUDE_SYNCER_NO_UPDATE_CHECK=1` to turn it off.

## Uninstall

```sh
claude-syncer uninstall          # stop the background agent (add --purge to remove state, trash, logs)
brew uninstall claude-syncer
```

Your sessions stay exactly as they are.

## Check on it

```console
$ claude-syncer status
✓ Background sync is on (claude-syncer 0.1.0, last ran 12s ago)
  claude-syncer 0.1.0
  Claude Desktop 6.6395.1
  Last sync 12s ago (840 ms): 4 accounts, 14 folders, 198 sessions
  All accounts in sync
  Disk: state 780 KB, trash 4.1 MB (kept 7 days), log 18 KB
```

Other commands:

| Command | |
|---|---|
| `claude-syncer log [-f]` | recent sync activity |
| `claude-syncer sync --dry-run` | show what a sync would change, change nothing |
| `claude-syncer sync` | sync now |

## What to expect

**The signed-in account catches up when you switch, or when Desktop restarts.**
Desktop holds the signed-in account's sessions in memory and only reads its
folder at start-up. Sessions created elsewhere appear there after a restart or
an account switch. Edits to its existing sessions are applied right after you
switch away. Every other account is always up to date.

**Deleting is real.** Deleting a session in one account deletes it in all of
them, and its terminal transcript goes too (as Desktop does when you only have
one account). Removed files are kept in
`~/Library/Application Support/claude-syncer/trash/` for 7 days. If one sync
would delete more than 20 sessions, it deletes nothing and asks you to confirm
with `claude-syncer sync --force`.

**Account settings stay with the account.** MCP servers, tool permissions,
Remote Control, scheduled tasks and published artifacts are not copied, only
the conversations.

**Old terminal sessions.** Claude Code deletes transcripts older than 30 days
by default. Their sessions stay listed in Desktop but cannot be reopened. To
keep transcripts longer, raise `cleanupPeriodDays` in
`~/.claude/settings.json`.

## How it works

A launchd agent runs one sync pass whenever a session file changes, and once
a minute. A pass compares every account's copy of every session with what it
saw last time, so it can tell a real edit from a stale copy Desktop wrote back
from memory, and merges field by field. See [docs/design.md](docs/design.md),
and [docs/desktop-internals.md](docs/desktop-internals.md) for the Desktop
behaviours it relies on and how they were verified.

## Development

```sh
make test      # go test -race ./...
make lint      # gofmt + go vet
make build     # ./claude-syncer
./claude-syncer sync --dry-run
```

The tests build fake Desktop folders in a temporary directory and never touch
real data. Releasing is described in [docs/releasing.md](docs/releasing.md).

## Prior art

These projects mapped much of the ground: deletion tombstones and launchd
setup ([claude-code-multi-account-bridge](https://github.com/ramonmazinga/claude-code-multi-account-bridge)),
the signed-in-account rule and account-local fields
([claude-session-sync](https://github.com/bdarbaz/claude-session-sync)), and
the imported-record format
([claude-desktop-session-sync](https://github.com/andiradulescu/claude-desktop-session-sync)).

## License

MIT
