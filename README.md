# Devlets Relay

Show your **Claude Code** and **Codex** usage limits on your Android home screen.

`devlets` is a small command-line tool for your computer. After each reply from Claude Code or Codex, it reads how much of your 5-hour and weekly limits you have left, encrypts that reading, and sends it to the [Devlets](https://getdevlets.com) app on your phone. There, the Claude Meter, Codex Meter and Fuel Cells widgets draw it as rings that drain while you work and refill when your limits reset.

- **End-to-end encrypted.** The key is made on your computer and passed to your phone by QR code. The server in the middle only ever holds ciphertext.
- **Never reads your logins.** It uses only the numbers Claude Code and Codex already give to their own hooks.
- **No background service.** It runs only when Claude Code or Codex does, for a moment after each reply.
- **Undo in one command.** `devlets unlink` puts your settings back exactly as they were.

## Install

macOS and Linux:

```sh
curl -fsSL https://getdevlets.com/link.sh | sh
```

Windows (PowerShell):

```powershell
irm https://getdevlets.com/link.ps1 | iex
```

The script downloads the release for your system, checks its SHA-256 against the hashes getdevlets.com pins for that release, puts `devlets` on your PATH and starts `devlets link`. It installs to `~/.devlets/bin` on macOS and Linux and `%LOCALAPPDATA%\Devlets\bin` on Windows. You can also download an archive from [Releases](https://github.com/joaquingalang/devlets-relay/releases) and [verify it](#verify-a-download) yourself.

Releases are built for Windows (x64, ARM64), macOS (one universal binary) and Linux (x64, ARM64).

## Quick start

1. **Install** with the command above. It runs `devlets link` for you.
2. **Say yes** to the tools you want to hook (Claude Code, Codex or both). Nothing on your computer changes until you do.
3. **Scan the QR code** in Devlets on your phone: Fuel Cells → Link a computer → Scan code. Your phone's camera app works too. The phone shows a 6-digit code (like `123 456`); tap Link only if it matches the one in your terminal.
4. **Keep working.** Codex shows up within a minute; Claude after your next Claude Code message. Run `devlets status` any time to see the last reading.

Don't share or stream the QR code or the link under it: they open your readings. The 6-digit code is only for comparing and isn't secret.

## What it sends

One small reading per computer, encrypted before it leaves. Before encryption it looks like this:

```json
{
  "v": 1,
  "computer": "MY-LAPTOP",
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

That's all of it: the computer's name, which tools are hooked, the percentages and when they reset. No prompts, no code, no files, no account details.

It sends at most once every five minutes, and only when a number has moved.

## How it works

```
Claude Code ─┐                                         ┌─ Devlets app
             ├─ devlets ── encrypted ──> mailbox ──────┤  (decrypts, draws
Codex ───────┘   (your computer)    api.getdevlets.com │   the widgets)
```

- **Claude Code.** With your yes, `devlets link` sets Claude Code's status line to `devlets claude-statusline`. Claude Code already passes that command your usage limits, so nothing else is read. If you had a status line, it still runs and its output is unchanged.
- **Codex.** With your yes, it sets `notify` in `~/.codex/config.toml` to `devlets codex-notify`, passing each call on to the program that was there before. When a reading is due, it asks Codex's own app server (`codex app-server`) for your rate limits, then stops it.
- **Encryption.** Each reading is sealed with AES-256-GCM using a 256-bit key created on your computer. The key reaches your phone only inside the QR code (and the link printed under it), in the part after `#`, which browsers never send to a server.
- **The mailbox** at `api.getdevlets.com` keeps only the newest encrypted reading for each linked computer. It deletes a mailbox when you unlink, when no phone claims it within 7 days, or after 90 days without use.

### What it never does

- Open `~/.claude/.credentials.json`, `~/.codex/auth.json`, your keychain or any other credential.
- Call Anthropic's or OpenAI's APIs.
- Keep running in the background, or start with your computer.
- Send anything the server can read.

## Commands

| Command | What it does |
|---|---|
| `devlets link` | Links this computer to your phone. If it's already linked, it can show the code again or start over. Flags: `--no-claude` and `--no-codex` skip a tool; `--yes` answers yes to every question. |
| `devlets status` | Shows this computer's name, which tools are hooked and when each was last read, and when the last reading was sent. |
| `devlets push` | Reads your usage and sends it now. |
| `devlets update` | Installs the newest release. |
| `devlets unlink` | Restores your Claude Code and Codex settings, deletes the mailbox and removes the local key. |
| `devlets --version` | Prints the version. |

`devlets claude-statusline` and `devlets codex-notify` are run by Claude Code and Codex. You don't need to run them yourself.

### Updates

`devlets status` mentions a newer release at most once a week. `devlets update` installs a release only when it matches the version and SHA-256 hashes that getdevlets.com publishes for it, so a tampered GitHub release can't install itself. Right after a new release, it may say the version isn't confirmed yet; try again later.

## Codex and command shells (Windows)

Devlets passes each Codex notification on to the notifier you had before. On Windows it won't do that when the previous notifier is a `.bat` or `.cmd` file, or runs through `cmd.exe`, `powershell.exe` or `pwsh.exe`, because those programs could run parts of the message as commands. A notifier started through `npx` is one of these (`npx.cmd`). `devlets link` and `devlets status` tell you when this applies, or when your previous notifier can't be found; it won't run while Devlets is linked, and `devlets unlink` restores it.

## Verify a download

Every release is built by GitHub Actions from its tag, with signed build provenance. To check an archive you downloaded:

```sh
gh attestation verify devlets_0.2.0_linux_amd64.tar.gz --repo joaquingalang/devlets-relay
```

Each release also has a `checksums.txt` with the SHA-256 of every archive.

## Uninstall

1. Run `devlets unlink`. It restores your Claude Code and Codex settings, deletes the mailbox and removes the stored key and token.
2. Delete the install folder: `~/.devlets` on macOS and Linux, or `%LOCALAPPDATA%\Devlets` on Windows.
3. Optional: remove the `PATH` line from your shell profile (macOS and Linux) or the `...\Devlets\bin` entry from your user Path (Windows).

Settings live in `%APPDATA%\Devlets` on Windows, `~/Library/Application Support/Devlets` on macOS and `~/.config/devlets` on Linux; `devlets unlink` empties them.

## Build from source

You need Go 1.27 or newer.

```sh
git clone https://github.com/joaquingalang/devlets-relay.git
cd devlets-relay
go test ./...
go build -o devlets ./cmd/devlets
```

A build from source reports its version as `dev` and never updates itself.

## Help and security

- Questions and bugs: [open an issue](https://github.com/joaquingalang/devlets-relay/issues) or email support@getdevlets.com.
- Security problems: please email support@getdevlets.com rather than opening a public issue.
- More about the app: [getdevlets.com/relay](https://getdevlets.com/relay) and the [privacy policy](https://getdevlets.com/privacy).

Devlets is an independent app, not affiliated with or endorsed by Anthropic or OpenAI. Claude, Claude Code and Codex are trademarks of their owners.

## Licence

MIT. See [LICENSE](LICENSE).
