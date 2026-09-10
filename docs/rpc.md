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
With the patched backend it includes `collection_protocol: "bounded_rows_v1"`.
Collectors must require this capability before interpreting range completion.

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
The backend reads at most `limit` recent messages in one invocation, not the
larger `max_message_scan` lookup bound. Local suppression may return fewer
messages (even none); a full backend page always yields `scan_complete: false`.
It does not trigger extra scans to fill filtered slots.

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

It returns ascending messages, an opaque replacement cursor, `more`, and
`range_complete`. Exactly one of the latter two flags is true. The first page
captures an inclusive upper row boundary; a pending cursor preserves that
boundary across retries, pages and broker restarts. `more: true` requests another
page. `range_complete: true` means every existing insertion row in that bounded
range has been examined under the applicable visibility rules; using the
completed cursor again begins the next range. It does not assert cloud-sync
freshness, coverage before the chosen seed, or capture of later edits/deletions.

The backend scans at most `min(limit, max_collection_scan)` physical rows, so
filtered messages can leave a short or empty page. Those rows still advance the
cursor. A quiet database returns a successful empty completed page, not an error.
The page size bounds work, not the total backlog; there is no watch-window
overflow. `after_row_id: 0` explicitly starts before the first row; generation
is still required. Never combine the two cursor forms or derive a collection
boundary implicitly from newest-first history.

Persist messages and cursor atomically. Failed pages never supply a replacement
cursor. New cursors encrypt/authenticate row boundaries using the persistent
owner-only key next to the policy file. They are bound to instance, account and
database generation. Existing v1 cursors resume from their same exclusive row
and upgrade to the new format. Unix peer credentials remain authentication;
neither cursors nor range flags grant visibility.

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
`collection_unsupported`,
`collection_overflow`, `collection_incomplete`, `backend_invalid`,
`backend_unavailable`, `backend_timeout`, and `response_too_large`.

The configured `backend_timeout_ms` bounds the entire data request, including
all per-chat backend lookups. Exhausting it returns retryable `backend_timeout`,
without partial results or a replacement collection cursor. Shutdown cancels
requests and joins backend processes. The client preserves integer IDs exactly
and cancels socket I/O when its context is canceled.
