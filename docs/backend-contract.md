# Audited imsg backend contract

The adapter is based on OpenClaw `imsg` tag `v0.13.1`, commit
`6918867c6439298103df592d09835fdfda51a090`. Collection requires the reviewed
[native overlay](../backend/imsg/README.md), version `0.13.1-safe-imsg.2`.
The original version remains supported for other reads, not collection.

It executes exactly these process shapes, with the executable and database
paths supplied only by owner configuration:

```text
imsg chats   --db DATABASE --limit N --json
imsg group   --db DATABASE --chat-id ID --json
imsg history --db DATABASE --chat-id ID --limit N --json
imsg collect --db DATABASE --since-rowid ROW --limit N --account-id ACCOUNT --json [--through-rowid UPPER] [--not-before RFC3339]
```

It never starts `imsg rpc` and never invokes send, react, read, typing, account,
attachment, bridge, scheduled, group mutation, or arbitrary caller-selected
commands. Backend stderr and error text are discarded. Exit failures and
malformed, oversized, excessive, or contradictory output become fixed broker
errors.

The adapter relies on these audited facts:

- chat JSON includes row ID, identifier, GUID, service, native account ID, `is_group`, and
  external participants;
- history is newest-first and bounded;
- history's `--participants` filters message senders, not chat membership, so
  the broker never uses it for authorization;
- upstream message JSON may expand reply bodies and may contain attachments,
  reactions, previews, polls, display names, and routing data.

Only the narrow Go `RawChat` and `RawMessage` fields are decoded. The outbound
types cannot contain reply expansion, attachments or paths/names, reactions,
previews, polls, arbitrary rich payloads, display/contact names, unread counts,
or backend errors.

## Bounded collection extension

There is no watch fallback. The exclusive starting row is nonnegative; zero
explicitly includes the first retained row. The first page captures a fixed
inclusive upper row ID. Every page selects ascending physical rows before
filtering, in a read-only transaction covering metadata and the completion
probe. No snapshot is held between requests.

On an initial zero-row request only, `--not-before` finds the earliest physical
row whose own message timestamp meets the requested horizon, within the same
fixed snapshot. It does not filter later interleaved rows by date: the durable
consumer cutoff does that before journaling. If no row meets the horizon, the
current upper boundary is returned complete without scanning old content.

Every scanned row emits a JSONL envelope, even when skipped as a reaction,
non-text app event, orphan or foreign-account row. Ambiguous chat links and
invalid required metadata fail closed. Ordinary rows decode only their own
bounded text/attributed body and narrow chat metadata, without reply,
attachment, contact, poll, transcription or preview enrichment.

The final `safe-imsg.collect.v1` checkpoint supplies `through_row_id`,
`scanned_through_row_id` and `complete`. Go requires this footer, successful
process exit, strict row ordering, unchanged bounds and consistent metadata.
Malformed/missing/oversized output, excess rows, timeout or cancellation returns
no page. Silence never proves completion. Filtered-only pages advance; empty
or exactly full terminal pages can prove range completion.

Policy reloads before serialization. Both positions are encrypted in durable
owner-keyed cursors. Physical scans do not exceed the requested result limit,
so no admitted row is skipped to fit a response. Large backlogs are paginated.
Completion covers retained insertion rows, not edits, rows deleted before
reading, cloud synchronization or history newly granted by later policy changes.

Re-audit these assumptions before changing the pinned backend version.
