# MatterMail — Development Roadmap

Bridging email (SMTP/IMAP) and chat networks through [matterbridge](https://github.com/42wim/matterbridge):
send messages to a WhatsApp (or any supported) group by email, and mirror restricted groups to email addresses.

## Architecture decision (research summary)

matterbridge has **no runtime plugin system**. There are exactly two ways to add a protocol:

1. **In-tree Go bridge** — fork matterbridge, add `bridge/email/`, register in `gateway/bridgemap/`,
   rebuild. Tight coupling to matterbridge release cycle; upstream (42wim) is stalled and the
   community fork (`matterbridge-org/matterbridge`) is active but such a PR has high friction.
2. **External bridge via the matterbridge `api` bridge (chosen)** — a standalone process that talks
   to matterbridge's built-in HTTP/WebSocket API. Zero changes to matterbridge, works with any
   existing binary/docker image, same pattern used by the established 3rd-party bridges
   (matterdelta for Delta Chat, matterbabble for Discourse, fbridge for Messenger, etc.).

```
                IMAP (poll/IDLE)                     POST /api/message
  Mail server ─────────────────────▶ ┌───────────┐ ───────────────────▶ ┌──────────────┐
   (INBOX)                           │           │                      │  matterbridge │──▶ WhatsApp
                                    │ mattermail│                      │   (api bridge)│◀── Telegram
  Recipients  ◀─────────────────────│           │ ◀────────────────── │              │──▶ Mattermost…
                SMTP send            └───────────┘   WS /api/websocket  └──────────────┘
```

Key API facts (verified against `bridge/api/api.go` in matterbridge):

- `POST /api/message` — injects a message into **every gateway that contains the api account**.
  Server forces `channel="api"`, `protocol="api"` and stamps account/timestamp. Auth (optional):
  `Authorization: Bearer <Token>`.
- `GET /api/websocket` — melody WebSocket; receives every message the gateways relay to the api
  bridge (except our own posts — matterbridge does not loop back to the origin bridge).
  First frame is a greeting `{"event":"api_connected"}`.
- `GET /api/stream` — **avoid**: it *drains* a shared ring buffer (single-consumer, lossy when
  combined with other readers) and only carries messages if `Buffer` > 0.
- `GET /api/messages` — drains the ring buffer (potential catch-up mechanism, see Phase 2).
- Messages are JSON of matterbridge `config.Message`; files travel in
  `extra.file[]` with base64 `data`.

Routing model:

- **Outbound (chat → email):** WS messages carry `gateway` (gateway name) and `account` (origin
  bridge). Routes match on `gateway` (optionally `channel`) and send the composed email to
  `route.to` via SMTP.
- **Inbound (email → chat):** mail found in the watched IMAP folder is filtered, converted and
  POSTed to `/api/message`; matterbridge fans it out to all gateways containing the api account.
  Consequence: **one api account = one fan-in domain**. Multiple independent email↔group pairs
  need multiple api accounts (Phase 3).

### Prior art surveyed (nothing to fork directly)

| Project | What it is | Takeaway |
|---|---|---|
| [matterdelta](https://github.com/adbenitez/matterdelta) | Delta Chat ↔ matterbridge via api bridge | Reference for api-bridge client pattern |
| [Virtomize/mail2most](https://github.com/Virtomize/mail2most) | IMAP → Mattermost (Go, maintained) | IMAP watching, filters, HTML→Markdown |
| [etkecc/postmoogle](https://github.com/etkecc/postmoogle) | Matrix ↔ email (Go, maintained) | SMTP server mode, threading, DKIM concerns |
| [rodcorsi/mattermail](https://github.com/rodcorsi/mattermail) | IMAP → Mattermost (archived 2019) | Name collision only; unrelated to matterbridge |

No existing SMTP/IMAP bridge exists inside the matterbridge ecosystem — this project fills that gap.

---

## Phase 0 — Foundations ✅ (2026-02)

- [x] Research matterbridge extension mechanisms; pick api-bridge architecture
- [x] Survey existing email bridges; collect reference implementations
- [x] Create repository `rodrigoesborges/mattermail`
- [x] This roadmap, README, MIT license, Go module skeleton

## Phase 1 — MVP: bidirectional text + attachments (v0.1)

Goal: one mailbox ↔ one matterbridge gateway, both directions, deployable via Docker.

- [x] TOML configuration with env-var expansion (`${VAR}`) and validation
- [x] matterbridge client: `POST /api/message` (send) + WebSocket receive with auto-reconnect
- [x] IMAP watcher: poll for `UNSEEN`, fetch raw MIME, mark `\Seen` **only after** successful
      delivery (at-least-once semantics), reconnect with backoff
- [x] MIME parsing: text/plain preferred, HTML→text fallback, RFC 2047 subjects, attachments
      (size-capped) forwarded as `extra.file[]`
- [x] SMTP sender: implicit TLS + STARTTLS + plain, outgoing attachments, loop-prevention header
- [x] Outbound routes keyed by `gateway` (optional `channel`), subject/body templates
- [x] Loop prevention: skip mail bearing our marker header or coming from our own address
- [x] Inbound filters: `allowed_from` (exact address or `@domain` / `*.domain` globs)
- [x] Graceful shutdown (SIGINT/SIGTERM), structured logging (`log/slog`)
- [x] Unit tests (parsing, composing, routing, templates, config)
- [x] Dockerfile (scratch/distroless, multi-stage) + docker-compose example
- [x] Sample `docs/matterbridge.toml` (api account + gateway + WhatsApp)
- [x] CI: build + test + vet on push
- [ ] Manual end-to-end test against a real mailbox + matterbridge

## Phase 2 — Robustness & fidelity (v0.2)

- [ ] IMAP IDLE with unilateral-update wake-ups; automatic fallback to polling when the server
      misbehaves (IDLE support detection via capability `IDLE`/IMAP4rev2)
- [ ] Catch-up after reconnect: drain `GET /api/messages` ring buffer once, then attach WS;
      de-duplicate by message `id`/timestamp window
- [ ] Threading: map email `Message-ID`/`In-Reply-To` ↔ matterbridge `parent_id` where protocols
      support it; stable `Subject:` reuse for reply threading on the mail side
- [ ] HTML outbound mail (multipart/alternative: text + rendered simple markdown)
- [ ] Inline image handling (content-id ↔ attachment correlation) both directions
- [ ] Message size limits, truncation notices, configurable max attachment size per route
- [ ] Metrics counters (mails in/out, errors, lag) exposed on a local HTTP endpoint (`/metrics`,
      Prometheus text format) + `/healthz`
- [ ] Fuzz the MIME parser (`go test -fuzz`)

## Phase 3 — Multi-tenancy & operations (v0.3)

- [ ] Multiple IMAP/SMTP accounts and multiple matterbridge api connections in one process
      (enables independent email↔group pairs with isolated fan-in domains)
- [ ] Per-route inbound address matching (`To`/`Cc`/`Delivered-To` aliases → specific gateway)
      using multiple api accounts
- [ ] State store (JSON/BoltDB) for UID validity, processed Message-IDs, dedup across restarts
- [ ] systemd unit, `-verify-config` flag, `-once` flag (drain and exit)
- [ ] Rate limiting and anti-loop circuit breaker (max mails/min, bounce backoff)
- [ ] Optional self-test command: `mattermail check` validates IMAP/SMTP/api connectivity

## Phase 4 — Distribution & community (v1.0)

- [ ] Versioned releases via GoReleaser (linux/amd64,arm64; macOS; Windows) + SBOM + cosign
- [ ] Multi-arch container images (`ghcr.io/rodrigoesborges/mattermail`)
- [ ] Docs site/README expansion: per-provider notes (Gmail app passwords, Office365, Fastmail,
      self-hosted Dovecot), security notes (SPF/DKIM alignment for outbound, restriction models)
- [ ] Example Tengo scripts for subject-based routing on the matterbridge side
- [ ] Announcement to matterbridge community (wiki "3rd party bridges" list PR, matterbridge chat)

## Phase 5 — Ecosystem options (exploratory, post-1.0)

- [ ] Optional in-tree `bridge/email` for the community fork (`matterbridge-org/matterbridge`),
      sharing the mail-format packages from this repo (`internal/` → importable module)
- [ ] Mailing-list mode: one gateway ↔ moderated mailing list (member management, digest)
- [ ] SMTP-receive mode (mattermail itself listens on :25, à la postmoogle) for zero-IMAP setups
- [ ] i18n of system notices, template library (digest, quiet hours)

## Non-goals

- Replacing a mail transfer agent or mailing-list manager (Mailman/MLM features)
- Reading/writing arbitrary mailboxes beyond the configured folder
- Supporting matterbridge forks that remove the api bridge

## Risk register

| Risk | Mitigation |
|---|---|
| Email loops (bridge ↔ autoreply/MLM) | marker header + own-address skip; Phase 3 circuit breaker |
| matterbridge api token absent on LAN deployments | document loopback binding; token recommended |
| Slow IMAP providers (Gmail polling limits) | Phase 2 IDLE; poll interval guidance |
| WS message loss during mattermail downtime | Phase 2 ring-buffer catch-up; note matterbridge `Buffer` option |
| Name collision with archived `rodcorsi/mattermail` | distinct description/branding: "MatterMail — email bridge for matterbridge" |
