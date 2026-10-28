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

## Pairing

`devlets link` prints a QR code, a link and a 6-digit confirm code such as `123 456`. Scan the QR code in the Devlets app. If the phone shows a code, it must match the one in your terminal. Don't share or stream the QR code or the code: they open your readings.

## Codex and command shells

Devlets runs after each Codex reply and passes the reply's message on to the notifier you had before. On Windows it won't do that when the notifier is a `.bat` or `.cmd` file, or runs through `cmd.exe`, `powershell.exe` or `pwsh.exe`, because those programs would read parts of the message as commands. `devlets link` and `devlets status` tell you when your previous notifier is one of these, or can't be found. It won't run while Devlets is linked. A notifier started through `npx` is one (`npx.cmd`).

## Verify a download

Releases carry build provenance. To check an archive you downloaded:

```sh
gh attestation verify <archive> --repo joaquingalang/devlets-relay
```

## Commands

- `devlets link` links this computer to your phone
- `devlets status` shows what is linked and the last reading
- `devlets unlink` removes the link and the hooks
- `devlets push` reads usage and sends it now
- `devlets update` installs the latest version (`status` mentions a newer one at most once a week). It installs only versions getdevlets.com confirms: the release must match the version and hashes that site publishes, so right after a release it may say the new version isn't confirmed yet
- `devlets --version` prints the version

## Licence

MIT. See [LICENSE](LICENSE).
