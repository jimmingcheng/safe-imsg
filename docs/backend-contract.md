# Audited imsg backend contract

The v1 adapter is based on OpenClaw `imsg` tag `v0.13.1`, commit
`6918867c6439298103df592d09835fdfda51a090`.

It executes exactly these process shapes, with the executable and database
paths supplied only by owner configuration:

```text
imsg chats   --db DATABASE --limit N --json
imsg group   --db DATABASE --chat-id ID --json
imsg history --db DATABASE --chat-id ID --limit N --json
imsg watch   --db DATABASE --since-rowid ROW --debounce 0ms --json
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
- watch `--since-rowid` is exclusive; the value zero means “start at newest,”
  so safe-imsg requires a positive value;
- watch reads physical rows in ascending row-ID order in batches of 100;
- its fallback poll interval is five seconds and it publishes no high-water
  mark for a batch that produces no messages;
- upstream message JSON may expand reply bodies and may contain attachments,
  reactions, previews, polls, display names, and routing data.

Only the narrow Go `RawChat` and `RawMessage` fields are decoded. The outbound
types cannot contain reply expansion, attachments or paths/names, reactions,
previews, polls, arbitrary rich payloads, display/contact names, unread counts,
or backend errors.

The broker collects a bounded observed prefix: it waits 1.5 seconds for an
initial row, then ends the window after 500 ms without another row. It drains
buffered output and waits for the process before returning. Only a deliberate
broker stop is successful; an unexpected clean exit also fails. If no rows were
observed it returns `collection_incomplete`. Thus the overflow bound applies to
observed rows, not to an unknowable total database backlog. Consumers must not
interpret any watch window as proof of a complete database replay.

Re-audit these assumptions before changing the pinned backend version.
