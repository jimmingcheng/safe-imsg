# Broker RPC v1

The transport is one request and one response per Unix connection. Each JSON
object is prefixed by a four-byte unsigned big-endian length. Requests larger
than 1 MiB and responses larger than 8 MiB are rejected. Unknown envelope
fields, parameter fields, methods, and protocol versions are rejected.

Request envelope:

```json
{"v":1,"id":"caller-id","method":"system.ping","params":{}}
```

Success and error envelopes:

```json
{"v":1,"id":"caller-id","ok":true,"result":{}}
{"v":1,"id":"caller-id","ok":false,"error":{"code":"invalid_params","message":"...","retryable":false}}
```

## Methods

`system.ping {}` returns `{"pong":true}`.

`system.info {}` returns the instance, public account ID, effective database
generation, maximum result count, protocol version, and this exact method list.

When the optional Contacts source is configured, it also returns
`contacts_policy` with `state` (`ready`, `degraded`, or `unavailable`) and optional
RFC 3339 `last_attempt_at`, `last_success_at`, `expires_at`, and a fixed
`last_error_code`. It contains no list names/IDs, contact counts or identities.
`degraded` means a transient refresh error with an unexpired snapshot still in
use. `unavailable` makes data operations return `policy_unavailable`, without
a replacement collection cursor. This is local-store health, not proof of
upstream iCloud/Contacts Sync freshness. No Contacts management RPCs are added.

`imsg.list_chats {"limit":20}` returns:

```json
{"chats":[{"account_id":"apple-personal","database_generation":"dbgen_...","chat_id":42,"chat_guid":"iMessage;+;...","service":"iMessage","is_group":true,"participants":["+14155550100","friend@example.com"]}],"scan_complete":false}
```

`imsg.history` requires `chat_id` and `database_generation`; `limit` is
optional. It returns newest-first admitted messages and `scan_complete` for the
bounded scan and result limit: false means additional admitted messages may
exist. An omitted limit is capped by the configured `max_results`.

`imsg.get_message` requires `chat_id`, `database_generation`, and exact message
`guid`. Lookup is limited to `max_message_scan` recent rows.

`imsg.collect` accepts either:

```json
{"after_row_id":9000,"database_generation":"dbgen_...","limit":50}
```

or:

```json
{"cursor":"PREVIOUS_CURSOR","limit":50}
```

It returns ascending messages, a replacement cursor, and `more`. A true value
requests an immediate follow-up probe. No observable rows in the watch window
produces retryable `collection_incomplete`, with no cursor advancement. Keep the
previous cursor and retry later. This may also indicate upstream-suppressed
batches; v1 cannot guarantee a full replay through such a batch. Never combine
the two cursor forms. Cursors are account- and database-generation-bound, but
are not authentication tokens; Unix peer credentials provide authentication.

## Message fields

Every admitted message contains only:

```text
account_id, database_generation, row_id, chat_id, chat_guid,
guid, sender (inbound only), from_me, text, created_at
```

Likely authentication-code and sign-in-link messages are omitted locally. No
suppression count is returned.

Stable error codes include `invalid_request`, `invalid_params`,
`unsupported_version`, `unauthorized_peer`, `peer_auth_failed`,
`method_not_allowed`, `policy_unavailable`, `not_visible`, `not_found`,
`lookup_incomplete`, `stale_generation`, `stale_cursor`,
`collection_overflow`, `collection_incomplete`, `backend_invalid`, `backend_unavailable`, and
`response_too_large`.

The configured `backend_timeout_ms` bounds the entire data request, including
all per-chat backend lookups. Shutdown cancels requests and joins backend
processes. The client preserves integer IDs exactly and cancels socket I/O when
its context is canceled.
