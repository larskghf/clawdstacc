---
name: Bug report
about: Something isn't working as expected
title: "[bug] "
labels: bug
---

**Describe the bug**
A clear and concise description of what the bug is.

**Steps to reproduce**
1. ...
2. ...
3. See error

**Expected behaviour**
What you thought would happen.

**Actual behaviour**
What actually happened.

**Diagnostics**
```
# Output of:
./bin/clawdstacc status
```

```
# Tail of relevant logs:
tail -50 ~/Library/Logs/clawdstacc/<service>.err
```

**Environment**
- macOS version + chip (e.g. Sonoma 14.5, Apple Silicon M2)
- clawdstacc version (commit SHA or release tag)
- Relevant tool versions: `tmux -V`, `code-server --version`, `claude --version`, `go version`

**Additional context**
Anything else relevant — VPN setup, Cloudflare Tunnel, custom config, etc.
