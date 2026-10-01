# Kueiu Bot

Private Telegram assistant for a Notion database, powered by Azure OpenAI Responses.

## Setup

1. Create a private Notion integration, share the target database with it, and set `NOTION_TOKEN` and `NOTION_DATABASE_ID` in `.env`.
2. Set `TELEGRAM_BOT_TOKEN` in `.env`.
3. Set `AZURE_OPENAI_ENDPOINT`, `AZURE_OPENAI_API_KEY`, and `AZURE_OPENAI_DEPLOYMENT`. The endpoint should be the Azure OpenAI v1 base URL ending in `/openai/v1/`; confirm the selected Azure resource and deployment support the Responses API and function tools.
4. Add the bot to any chat type where it should respond. The application accepts user messages across private chats, groups, supergroups, and channels; channel posts require the bot to have permission to read and reply. To receive every group message without a mention, disable privacy mode using BotFather's `/setprivacy`, then remove and re-add the bot to the group.

Non-secret defaults belong in `config.yaml`. The Telegram token, Azure OpenAI API key, and Notion PAT are read only from `.env` or the process environment, never from config files or CLI flags. `.env` is git-ignored.

## Run

```sh
go run . serve
```

Flags can override non-secret config and environment values, for example `--azure-openai-deployment my-deployment` or `--config ./config.yaml`.

The bot processes delivered user messages and identifiable user-authored channel posts across chat types without requiring a mention; it removes a bot mention when it appears as a Telegram entity. Replies stay in the originating chat or topic. Each request is independent; replies include only the replied-to message and the current request as context.

## Build a Linux binary

Build with Go 1.23 or newer. To cross-compile a Linux AMD64 binary (for ARM64, set `GOARCH=arm64`):

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/kueiu-bot .
```

The output is `dist/kueiu-bot`. Copy it to the Linux host along with `config.yaml` and the environment file described below.

## Publish binaries on GitHub Releases

After the [release workflow](.github/workflows/release.yml) is merged into the default branch, push a version tag to trigger it. The workflow builds Linux AMD64 and ARM64 binaries, generates `SHA256SUMS`, and attaches them to a GitHub Release with generated release notes:

```sh
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

Replace `v1.0.0` with the next version. Once the workflow finishes, find the release under the repository's **Releases** page. Download both binaries and `SHA256SUMS` into the same directory to verify them with `sha256sum -c SHA256SUMS`, then use the binary matching the host architecture.

## Run as a Linux systemd service

The example unit at [`deploy/kueiu-bot.service`](deploy/kueiu-bot.service) expects the binary at `/usr/local/bin/kueiu-bot`, non-secret configuration at `/etc/kueiu-bot/config.yaml`, and secrets in `/etc/kueiu-bot/kueiu-bot.env`.

On the Linux host, install the files and create the service account:

```sh
sudo useradd --system --user-group --home-dir /nonexistent --shell /usr/sbin/nologin kueiu-bot
sudo install -d -o root -g root -m 0755 /etc/kueiu-bot
sudo install -o root -g root -m 0644 dist/kueiu-bot /usr/local/bin/kueiu-bot
sudo install -o root -g root -m 0644 config.yaml /etc/kueiu-bot/config.yaml
sudo install -o root -g root -m 0600 /dev/null /etc/kueiu-bot/kueiu-bot.env
sudoedit /etc/kueiu-bot/kueiu-bot.env
```

Add these values to the environment file (do not commit it):

```dotenv
TELEGRAM_BOT_TOKEN=...
AZURE_OPENAI_API_KEY=...
NOTION_TOKEN=...
```

The unit loads the environment file through systemd and starts the bot with the explicit config path. Install and start it with:

```sh
sudo install -o root -g root -m 0644 deploy/kueiu-bot.service /etc/systemd/system/kueiu-bot.service
sudo systemctl daemon-reload
sudo systemctl enable --now kueiu-bot
sudo journalctl -u kueiu-bot -f
```

Update Azure OpenAI deployment/endpoint and the Notion database ID in `/etc/kueiu-bot/config.yaml`, then restart with `sudo systemctl restart kueiu-bot`.

## Group troubleshooting

Send `@KueYuBot hello` in the group, selecting the bot from Telegram's mention suggestions. Telegram's privacy mode does **not** deliver plain @mentions to non-admin bots. To handle group mentions, use BotFather's `/setprivacy` to disable privacy for `@KueYuBot`, then remove and re-add it to the group. Alternatively, making the bot a group administrator allows it to receive all messages. The startup log's `can_join_groups` and `can_read_all_group_messages` fields show the bot's current BotFather settings; for a non-admin bot, `can_read_all_group_messages` must be `true` to receive plain mentions.

Watch the logs after sending the mention:

- No `Telegram polling received first group message`: Telegram did not deliver a group update. Confirm privacy is disabled (or the bot is an administrator), the bot is a member of the group, and the mention targets its exact username. If you changed `/setprivacy`, remove and re-add the bot so the change takes effect. With privacy still enabled, try `/start@KueYuBot` to confirm that commands addressed to the bot are delivered.
- `processing accepted message` but no `Telegram reply sent`: Check `Telegram reply failed` for Telegram's error (for example, missing permission to post in the group or topic), or `assistant request failed` for an assistant-service error.
- `ignored bare bot mention without request or reply context`: Include text after the mention or reply to a message with the mention.
