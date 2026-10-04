# Minimal tutorial: email ↔ WhatsApp group in ~15 minutes

This walkthrough bridges one mailbox to one chat group end-to-end. It assumes you already run
matterbridge with a working chat account (WhatsApp here, but any network works the same way).

**What you need**

- matterbridge running (v1.26+) with one chat account you can test with
- a dedicated mailbox, e.g. `bridge@example.com` (IMAP + SMTP)
- Go 1.24+ **or** Docker

## 1. Expose matterbridge's api bridge

Add to your `matterbridge.toml` (full example in [`docs/matterbridge.toml`](matterbridge.toml)):

```toml
[api.mattermail]
BindAddress = "127.0.0.1:4242"
Buffer = 1000
Token = "change-me"          # pick a long random string

[[gateway]]
name = "friends"             # any name; mattermail references it later
enable = true

[[gateway.inout]]
account = "whatsapp.friends" # your existing chat account
channel = "Family"           # the group you want to bridge

[[gateway.inout]]
account = "api.mattermail"
channel = "api"
```

Restart matterbridge. It now listens on `127.0.0.1:4242` and forwards everything between the
`Family` group and the api bridge.

## 2. Get MatterMail

```sh
git clone https://github.com/rodrigoesborges/mattermail.git
cd mattermail
go build .
```

(Or skip to step 4 and use `docker compose up -d` with the included `docker-compose.yml`.)

## 3. Configure it

Create `mattermail.toml` next to the binary:

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
token = "change-me"           # same value as [api.mattermail].Token

[mail_in]
allowed_from = ["@example.com"]   # only relay mail from your own domain for now

[[route]]
gateway = "friends"           # must match [[gateway]].name in matterbridge.toml
to = ["you@personal.org"]     # who receives group messages by email
```

Every string supports `${ENV_VAR}` expansion, so passwords can stay out of the file.

## 4. Run

```sh
export MATTERMAIL_IMAP_PASSWORD=... MATTERMAIL_SMTP_PASSWORD=...
./mattermail -config mattermail.toml
```

You should see `imap connected` and `websocket connected` within a few seconds.

## 5. Test both directions

**Email → chat**: send a normal email to `bridge@example.com` from an allowed sender. Within one
poll interval (30 s) the message appears in the `Family` group, prefixed with the sender's name.

**Chat → email**: write something in the `Family` group. Within seconds it lands in
`you@personal.org` with subject `[friends] Nickname`.

If a message shows up on the wrong side only, re-check that the `[[route]].gateway` name matches
`[[gateway]].name` exactly (case-sensitive in matterbridge, matched case-insensitively by
MatterMail).

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `dial ... connection refused` (IMAP/SMTP) | wrong `server` host/port; try `openssl s_client -connect host:993` |
| `401` / `unauthorized` on POST | `token` differs from matterbridge's `[api.mattermail].Token` |
| WebSocket connects, nothing arrives | gateway name in `[[route]]` doesn't match matterbridge's |
| Email arrives but never appears in chat | sender blocked by `allowed_from` |
| Mail reprocessed every poll | delivery to matterbridge failing; raise log level to `debug` in `[log]` |
| Two groups both receive email | the api account is in two gateways; keep one per account |

## Next steps

- Full option reference: [`mattermail.toml.sample`](../mattermail.toml.sample)
- Routing rules and loop prevention: [README](../README.md#how-routing-works)
- Architecture and roadmap: [ROADMAP.md](../ROADMAP.md)
