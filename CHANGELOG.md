# Changelog

## 0.1.1 (2026-11-19)

### Fixes

- Deleting many sessions at once in Claude Desktop no longer stops at the
  mass-delete brake. Deletions made in the app by the signed-in account are
  the user's own and are carried out straight away. Copies that can only be
  removed after an account switch follow without stopping at the brake again.
- A session being deleted in Claude Desktop could be put back. Desktop
  removes the record a few seconds before it writes the tombstone, and a
  sync in between restored it. A record that disappears now waits two
  minutes for its tombstone.

## 0.1.0 (2026-11-19)

First release.

- Keeps Claude Desktop's Claude Code sessions identical across every account
  and organization: create, rename, archive, delete, and the conversation's
  progress.
- Brings terminal (`claude`) sessions into Claude Desktop, including
  `/rename`.
- Account settings (MCP servers, permissions, Remote Control, scheduled
  tasks) stay with their account.
- Background agent through launchd; `status`, `log` and `sync --dry-run`.
- Deletions go to a 7-day trash; a pass that would delete more than 20
  sessions waits for confirmation.
- Homebrew formula in `cookiezisg/tap`, with a once-a-day update notice.
