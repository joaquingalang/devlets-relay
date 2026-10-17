# devlets (relay CLI)

Sends your Claude Code and Codex usage to the Devlets app on your phone, so a widget can show it.

**What is sent.** One small reading after each reply, like this:

```json
{
  "v": 1,
  "computer": "JOAQUIN-PC",
  "sentAt": "2026-10-17T09:12:00Z",
  "hooked": ["claude", "codex"],
  "tools": {
    "claude": {
      "readAt": "2026-10-17T09:11:42Z",
      "windows": [
        { "id": "five_hour", "usedPercent": 38, "resetsAt": "2026-10-17T12:00:00Z" },
        { "id": "seven_day", "usedPercent": 61.5, "resetsAt": "2026-10-21T00:00:00Z" }
      ]
    },
    "codex": {
      "readAt": "2026-10-17T09:11:50Z",
      "windows": [
        { "id": "primary", "usedPercent": 22, "minutes": 300, "resetsAt": "2026-10-17T13:30:00Z" },
        { "id": "secondary", "usedPercent": 47, "minutes": 10080, "resetsAt": "2026-10-22T08:00:00Z" }
      ]
    }
  }
}
```

It is **encrypted end to end**: the key is created on your computer and shared with your phone when you link. The relay only stores ciphertext it cannot read.

**No credentials are ever read.** The CLI never opens `~/.claude/.credentials.json`, `~/.codex/auth.json`, a keychain or any token, and never calls a provider API. It only uses the numbers Claude Code and Codex hand to their documented hooks and interfaces.

## Install

macOS and Linux:

```sh
curl -fsSL https://getdevlets.com/link.sh | sh
```

Windows (PowerShell):

```powershell
irm https://getdevlets.com/link.ps1 | iex
```

More: https://getdevlets.com/relay

## Commands

- `devlets link` links this computer to your phone
- `devlets status` shows what is linked and the last reading
- `devlets unlink` removes the link and the hooks
- `devlets push` reads usage and sends it now
- `devlets update` installs the latest version (`status` mentions a newer one at most once a week)
- `devlets --version` prints the version

## Licence

MIT. See [LICENSE](LICENSE).
