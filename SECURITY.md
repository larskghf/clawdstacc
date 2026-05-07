# Security policy

## Reporting a vulnerability

If you've found a security issue in clawdstacc, please report it privately
through GitHub:

→ **[Open a private security advisory](https://github.com/larskghf/clawdstacc/security/advisories/new)**

This goes only to the maintainers, not to a public issue tracker. You'll get an
acknowledgement within 72 hours and a fix or mitigation timeline shortly after.

If for some reason you can't use private advisories, open a regular issue with
the `[security]` prefix in the title and **omit reproduction details until the
maintainers respond and a private channel is established**.

Please include:

- A description of the issue and its impact
- Steps to reproduce (in the private advisory)
- Affected version or commit SHA

## Threat model

clawdstacc is designed for **single-user / small-team** use on a personal
macOS host (typically a Mac mini at home), reachable over a VPN or a
Cloudflare Tunnel + Access. Specifically out of scope:

- **Multi-tenant security**. There's no isolation between tmux sessions or
  Claude Code processes — they all run as the host user.
- **Public internet exposure without an auth proxy**. The dashboard binds
  on `0.0.0.0` and has no built-in auth. Always put it behind Cloudflare
  Access, Tailscale, oauth2-proxy, or similar.
- **Hardened MCP / tool sandboxing**. Tools called by Claude Code run with
  the same privileges as the user.

## Defaults you should know

- `CODESERVER_AUTH="password"` by default. Generate a strong password
  (`openssl rand -base64 24`) before exposing code-server, OR set
  `CODESERVER_AUTH="none"` and protect with an upstream auth layer.
- The dashboard does not authenticate by itself. Don't expose port 8390 to
  the internet without an auth proxy in front.
- `--dangerously-skip-permissions` is **off** by default. Setting it via
  `CLAUDE_EXTRA_FLAGS` allows Claude to run any tool without confirmation
  prompts; only use this if you trust your prompts and your repos.

## What clawdstacc does not store

- No user prompts or assistant responses leave your host except via the
  Anthropic API (which Claude Code calls itself).
- The dashboard parses `~/.claude/projects/*.jsonl` locally; nothing is sent
  upstream.
- Pasted images are stored under `/tmp/clawdstacc/` and auto-deleted after
  one hour.
