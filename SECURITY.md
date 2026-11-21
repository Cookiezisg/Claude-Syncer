# Security

Claude-Syncer reads and writes Claude Desktop's session records and Claude
Code's transcripts on your Mac. It makes no network requests except one
anonymous request a day for the published Homebrew formula (to tell you
about updates; `CLAUDE_SYNCER_NO_UPDATE_CHECK=1` turns it off). It never
reads credentials and never sends session data anywhere.

## Reporting a vulnerability

Please report privately through
[GitHub security advisories](https://github.com/Cookiezisg/Claude-Syncer/security/advisories/new),
not in a public issue. Expect a first reply within a week.

Only the latest release is supported.
