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

`imsg.list_chats {"limit":20}` returns:

```json
{"chats":[{"account_id":"apple-personal","database_generation":"dbgen_...","chat_id":42,"chat_guid":"iMessage;+;...","service":"iMessage","is_group":true,"participants":["+14155550100","friend@example.com"]}],"scan_complete":false}
```

`imsg.history` requires `chat_id` and `database_generation`; `limit` is
optional. It returns newest-first admitted messages and `scan_complete` for the
underlying bounded scan.

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
requests an immediate follow-up probe; consumers still poll later after an
empty response because upstream exposes no definitive caught-up marker. Never combine
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
`collection_overflow`, `backend_invalid`, `backend_unavailable`, and
`response_too_large`.
