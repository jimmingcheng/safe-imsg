# safe-imsg v1

Status: implementation specification; implementation and live deployment are not yet verified.

Jimming authorized starting this separate repository at `~/repos/safe-imsg/` in the current Codex conversation. The originating intake observation is `obs_74b3762e9a113cf76bd1`. This document preserves the design discussed in that conversation for the request's implementation executor. It is not a new request or a deployment authorization.

## Purpose and boundary

Provide Donna filtered, read-only access to Jimming's personal Apple Messages account using the client/broker pattern of `/home/donna/repos/safe-gmail`. `imsg` remains the Mac-side Messages backend. A trusted `safe-imsgd` broker runs under the account-owning macOS user; a restricted `safe-imsg` client accesses only its documented API through an authenticated Unix socket. One account per broker instance in v1. Keep the implementation in its own Go module and Git repository. Do not create a remote repository or publish it without a separately configured destination.

The owner controls policy, backend paths and database identity. Clients cannot modify policy, choose a database, execute arbitrary commands, or forward arbitrary imsg RPC methods. Authenticate Unix peer credentials on macOS and Linux; unsupported platforms fail closed. Document socket/file ownership and distinct owner/client OS principals. Existing unrestricted maintenance SSH access remains a bypass until a later hardening step; filtered output alone is not a hard security boundary while that access exists.

## Conversation visibility

- Expose explicitly approved direct-message counterparts and all verified group conversations.
- Explicit excluded conversations override both grants.
- Normalize exact phone/email identities. Never authorize by display name, substring, domain wildcard or message sender alone. Own account aliases cannot authorize a DM.
- Use native conversation metadata to distinguish DMs and groups. Missing or contradictory metadata fails closed; participant count or chat title alone is insufficient.
- A verified group may contain non-allowlisted participants.
- Recheck policy for every response, including history, lookup and incremental collection. Revocation must not be defeated by cached authorization or cursors.

## Read-only surface and content minimization

Implement bounded chat listing, history, individual-message lookup and incremental collection with a closed request/response schema. No sending, typing indicators, read receipts, account administration, attachment retrieval or unrestricted search/RPC passthrough in v1.

Withhold likely authentication-code and sign-in-link messages locally, using deterministic conservative rules and tests. This is defense in depth, not a guarantee that every secret can be recognized. Do not send suppressed material to an LLM or log it.

Serialize only explicitly approved message fields. Omit automatic quoted/reply body expansion, attachments and attachment paths/names, previews, polls, reactions, contact display names and arbitrary rich payloads. Unfiltered backend stderr/errors must never reach clients or audit logs. Avoid timestamps/counts derived solely from denied messages where they reveal otherwise hidden activity. Preserve stable message GUID and account-scoped provenance for allowed messages.

## Backend and collection correctness

The audited upstream checkout is `/tmp/imsg-v0131-intake-audit`, tag `v0.13.1`, commit `6918867c6439298103df592d09835fdfda51a090`, from https://github.com/openclaw/imsg. Inspect its source for exact CLI/RPC schemas rather than assuming APIs exist. In particular:

- `--participants` filters message senders, not conversation membership.
- History is newest-first and bounded; taking its maximum row ID can silently skip backlog.
- Watch supports an exclusive `since_rowid` cursor, and defaults to new messages only if absent. Design explicit restart/resume behavior and handle overflow as an error, never a silent skip.
- Message row IDs and chat IDs are database-local; account and database-generation binding is necessary. Detect/reset or reject stale generation cursors explicitly.
- Backend payloads can automatically include reply text from another message. Drop these fields in v1.
- The RPC backend exposes mutating operations; the broker must not expose those methods to clients.

If upstream cannot support a claimed operation correctly within bounded resources, return an explicit supported error and document the limitation; do not silently truncate, omit failures or label a stub complete. Prefer a small usable implementation over an unbounded cached mirror. Avoid a direct SQLite replacement for imsg.

## Implementation acceptance

Use OpenClaw's native Codex app-server integration as required by the workspace. Create the two binaries, configuration examples, operator/client usage, threat model, Makefile and meaningful synthetic tests. Inspect safe-gmail for conventions; do not blindly copy its implementation or unrelated dependencies. Test actual broker/client serialization and backend argument construction using a fake imsg process. No personal message exports are necessary.

Cover allowed and denied DMs, own aliases, verified groups containing unknown members, explicit group exclusions, missing/contradictory metadata, policy revocation, malformed and oversized requests, unknown methods, unauthorized peer UID, suppressed codes/sign-in URLs, reply/attachment/error leakage, cursor replay/generation/overflow and backlog ordering. Validate socket lifecycle without unlinking an active socket or arbitrary file/symlink. Reject unsafe configuration and fail closed on backend failures. Run Go tests, race detector, vet, build and Darwin cross-compilation; report which platforms were actually executed.

Review and commit only this repo's verified task files. Live Mac deployment, personal-message ingestion, changes to Donna's existing messaging bridge, SSH removal and outgoing test messages are outside this first implementation task. Future integration should feed admitted personal messages as ambient evidence into Donna's existing unified intake, without auto replies or owner-command authority.
