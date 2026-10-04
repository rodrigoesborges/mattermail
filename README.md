# MatterMail

**Email (SMTP/IMAP) bridge for [matterbridge](https://github.com/42wim/matterbridge).**

MatterMail connects a mailbox to any chat network matterbridge supports (WhatsApp, Telegram,
Mattermost, Slack, IRC, Matrix, ...):

- **Email → chat**: mail arriving in the watched IMAP folder is relayed into your
  matterbridge gateway (e.g. a WhatsApp group).
- **Chat → email**: messages posted in a bridged group are delivered to configured email
  addresses via SMTP.

matterbridge has no runtime plugin system, so MatterMail is an *external bridge* that talks to
matterbridge's built-in [`api` bridge](https://github.com/42wim/matterbridge/wiki/Api) — no fork,
no patched binary, works with the stock matterbridge you already run.

```
                IMAP (poll)                          POST /api/message
  Mail server ────────────────▶ ┌───────────┐ ───────────────────▶ ┌──────────────┐
   (INBOX)                      │           │                      │  matterbridge │──▶ WhatsApp
                               │ mattermail│                      │   (api bridge)│◀── Telegram
  Recipients ◀──────────────── │           │ ◀────────────────── │              │──▶ Mattermost…
                SMTP send       └───────────┘   WS /api/websocket  └──────────────┘
```

See [ROADMAP.md](ROADMAP.md) for architecture notes, prior art and the development plan, or
[docs/TUTORIAL.md](docs/TUTORIAL.md) for a step-by-step getting-started tutorial.

## Quick start

1. Add an `api` account and gateway to `matterbridge.toml` (see [`docs/matterbridge.toml`](docs/matterbridge.toml)):

   ```toml
   [api.mattermail]
   BindAddress = "127.0.0.1:4242"
   Buffer = 1000
   Token = "change-me"

   [[gateway]]
   name = "friends"
   enable = true

   [[gateway.inout]]
   account = "whatsapp.friends"       # your existing chat account
   channel = "Family"                 # the group you want to bridge

   [[gateway.inout]]
   account = "api.mattermail"
   channel = "api"
   ```

2. Create `mattermail.toml` (see [`mattermail.toml.sample`](mattermail.toml.sample)):

   ```toml
   [imap]
   server = "imap.example.com:993"
   username = "bridge@example.com"
   password = "${MATTERMAIL_IMAP_PASSWORD}"
   poll_interval = "30s"

   [smtp]
   server = "smtp.example.com:465"
   username = "bridge@example.com"
   password = "${MATTERMAIL_SMTP_PASSWORD}"
   from = "bridge@example.com"
   tls = "ssl"

   [matterbridge]
   url = "http://127.0.0.1:4242"
   token = "change-me"

   [mail_in]
   allowed_from = ["@trusted.example.com", "alice@friend.org"]

   [[route]]
   gateway = "friends"                     # matterbridge gateway name
   to = ["alice@friend.org", "bob@other.org"]
   ```

3. Run:

   ```sh
   go build .
   ./mattermail -config mattermail.toml
   ```

   Or with Docker:

   ```sh
   docker compose up -d   # see docker-compose.yml
   ```

Now: email sent *to* `bridge@example.com` lands in the WhatsApp group, and everything typed in
the group is emailed to Alice and Bob.

## How routing works

- **Inbound (email → chat)**: every new unseen mail in the IMAP folder is filtered
  (`mail_in.allowed_from`), converted to a matterbridge message (sender becomes the nick) and
  POSTed to `/api/message`. matterbridge relays it to **every gateway the api account belongs
  to** — keep one chat destination per api account, or run several api accounts (Phase 3).
- **Outbound (chat → email)**: MatterMail keeps a WebSocket to `/api/websocket` and receives all
  messages relayed by the gateway. Each `[[route]]` matches a matterbridge `gateway` (optionally
  `channel`) and emails the message to `route.to`.

Loop prevention: outgoing mail carries a `X-Mattermail-Sent` header and is skipped on the way
in, as is any mail from our own address.

## Configuration reference

See the annotated [`mattermail.toml.sample`](mattermail.toml.sample). Highlights:

| Section | Purpose |
|---|---|
| `[imap]` | server, credentials, folder, poll interval, TLS |
| `[smtp]` | server, credentials, sender address, TLS mode (`ssl`/`starttls`/`none`) |
| `[matterbridge]` | api bridge URL and token |
| `[mail_in]` | username format, subject/attachment forwarding, sender allow-list |
| `[[route]]` | gateway → recipients mapping, subject/body templates |

All string values support `${ENV_VAR}` expansion.

## Security notes

- Bind the matterbridge api to localhost and set a `Token`.
- Outbound mail is sent as your SMTP user; be mindful of SPF/DKIM/DMARC alignment.
- Use an app-specific mailbox; MatterMail marks processed mail as read.
- Attachments are size-capped (`max_attachment_mb`, default 10).

## Development

```sh
go build ./...   # build
go test ./...    # unit tests
go vet ./...
```

Standard library `log/slog` for logging. Packages under `internal/`:
`config`, `mbapi` (matterbridge client), `imapwatch`, `smtpsend`, `mailfmt` (MIME in/out),
`router` (route matching and message conversion).

## License

[MIT](LICENSE)
