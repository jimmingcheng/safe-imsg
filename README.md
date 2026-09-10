# safe-imsg

`safe-imsg` gives a restricted local OS user filtered, read-only access to one
Apple Messages account. The trusted macOS account owner runs `safe-imsgd`; the
restricted user runs `safe-imsg` through an authenticated Unix socket.

The v1 API can ping the broker, report its public configuration, list bounded
visible chats, read bounded recent history, look up a recent message by GUID,
and collect rows after an explicit cursor. It cannot send messages, retrieve
attachments, search arbitrary data, invoke arbitrary `imsg` RPC methods, mark
messages read, or change policy.

## Build and verify

Go 1.24 or newer is required.

```sh
make build
make test
make race
make vet
make darwin-build
```

The implementation was audited against OpenClaw `imsg` v0.13.1, commit
`6918867c6439298103df592d09835fdfda51a090`. Collection requires the pinned
[native overlay](backend/imsg/README.md), version `0.13.1-safe-imsg.3`;
the original supports other reads only. See [the backend contract](docs/backend-contract.md).

## Trust boundary

Use distinct OS accounts:

- the owner account owns `chat.db`, the pinned `imsg` binary, configuration,
  policy, socket directory, and `safe-imsgd` process;
- the client account has no sudo or owner-account execution and can only reach
  the socket;
- the socket directory is owner-owned and not group/other-writable; a shared
  group may receive traversal permission and socket read/write permission;
- `safe-imsgd` authenticates the connecting UID using `SO_PEERCRED` on Linux
  and local peer credentials on macOS. Other platforms fail closed.

If the client retains unrestricted SSH access as the owner, can read the
Messages database, or can modify owner files, this is not a hard security
boundary. See [the threat model](docs/threat-model.md).

## Configuration

Copy [broker.sample.json](testdata/broker.sample.json) and
[policy.sample.json](testdata/policy.sample.json) into an owner-only directory.
All configured paths must be absolute. Files and the resolved `imsg` binary
must be owned by the broker user or root and must not be group/other-writable.
Parent directories must also protect these paths from client replacement;
root-owned sticky directories such as `/tmp` are allowed as ancestors. Config
and policy reads are bounded to 1 MiB and validate the opened file descriptor.
File symlinks are rejected, so configure the resolved path to a pinned `imsg`
executable rather than a Homebrew convenience symlink. Startup also executes
`--version` and requires the configured, audited version exactly. Startup creates
a persistent private cursor key beside the policy; include it in private backups.

The policy exposes:

- DMs whose single normalized external participant is in `allowed_direct`;
- every conversation carrying consistent native group metadata;
- neither kind when its exact chat GUID is in `excluded_conversations`.

Phone identities normalize to E.164. Email identities are exact and
case-folded. Display names, substrings, wildcard domains, a message sender by
itself, and the owner's aliases never grant access.

On macOS, an optional [Contacts-backed policy source](docs/contacts-policy.md)
can keep DM grants in sync with contacts on any list in one explicitly selected
account in the owner's local Contacts store. This is the default when
`group_ids` is omitted; supplying list IDs narrows it. Unlisted contacts remain
excluded. It uses a read-only native helper, owner-local preview, bounded
refresh/expiry, and no new client RPCs. Leave the `contacts` config absent to
retain static policy. Set up Contacts permission and review the selected lists
before enabling this source or backfilling messages.

Validate without starting a listener:

```sh
safe-imsgd config validate --config /Users/owner/.config/safe-imsg/broker.json
```

Start the owner-side broker:

```sh
safe-imsgd run --config /Users/owner/.config/safe-imsg/broker.json
```

`backend_account_id` must exactly match the native `account_id` shown by the
audited `imsg group --chat-id ... --json` output; chats for any other or missing
backend account fail closed. The database generation returned to clients is derived from `account_id`, the
operator's `database_generation` label, and the database device/inode. A live
database replacement fails closed. Change the configured label after an
intentional database reset as an additional operational marker.

## Client usage

Global `--socket` comes before the command:

```sh
safe-imsg --socket /Users/Shared/safe-imsg/personal.sock info
safe-imsg --socket /Users/Shared/safe-imsg/personal.sock chats --limit 20

safe-imsg --socket /Users/Shared/safe-imsg/personal.sock history \
  --chat-id 42 --database-generation dbgen_FROM_INFO --limit 50

safe-imsg --socket /Users/Shared/safe-imsg/personal.sock message \
  --chat-id 42 --database-generation dbgen_FROM_INFO --guid MESSAGE-GUID
```

Collection starts after an explicitly agreed nonnegative row ID. An admitted
history row is suitable for a limited scope; zero requests all retained rows
and must be an intentional backfill:

```sh
safe-imsg --socket /Users/Shared/safe-imsg/personal.sock collect \
  --after-row-id 9000 --database-generation dbgen_FROM_INFO --limit 50

safe-imsg --socket /Users/Shared/safe-imsg/personal.sock collect \
  --cursor CURSOR_FROM_PREVIOUS_RESPONSE --limit 50
```

Persist the cursor atomically with processing the response, including empty
pages. `more: true` continues the same fixed range; resume next run if your work
budget is exhausted. `range_complete: true` means that bounded local insertion
range is exhausted. Reusing its cursor starts the next range. Empty pages can
advance past filtered rows or finish a quiet range. Require
`info.collection_protocol == "bounded_rows_v2"` before collecting. An initial
`not_before` timestamp can select a date horizon without guessing a row from one
chat; consumers must keep filtering older interleaved message timestamps. Errors leave
the last committed cursor unchanged. Opaque cursors survive restarts and bind
the account, database generation and broker instance.

## Deliberate v1 limitations

- `imsg history` has no exact message-get primitive. `message` scans only the
  configured recent-history bound and returns `lookup_incomplete` instead of a
  false `not_found` when that bound is exhausted.
- `imsg chats` has no paging cursor. `scan_complete: false` says older visible
  chats may exist beyond the configured scan or requested result limit. History
  reads only the requested number of recent backend messages in one call,
  and sets `scan_complete: false` whenever that page is full. Local suppression
  can underfill the result; it does not cause additional history scans.
- Initialization never silently selects a starting scope or derives a global
  checkpoint from newest-first history; it remains an owner decision.
- Collection covers retained insertion rows, not edits, deleted rows, cloud
  synchronization or historical messages newly admitted by later policy changes.
  Range completion does not establish complete historical coverage.
- This repository does not deploy or modify OpenClaw, Donna's unified intake,
  SSH access, or Messages. The JSON client is suitable for a later native
  Codex app-server/OpenClaw ingestion wrapper, where admitted items must remain
  ambient evidence without automatic replies or owner-command authority.

See [operations](docs/operations.md) and the [RPC schema](docs/rpc.md) for the
complete contract.
