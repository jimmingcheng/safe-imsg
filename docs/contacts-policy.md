# Contacts-backed DM policy

The optional `contacts` source derives DM grants from contacts on any list
in one explicitly selected account in the broker owner's local macOS Contacts
store. This is the default when `group_ids` is omitted (or `null`); an explicit
nonempty `group_ids` array narrows selection. An empty array is rejected, not
interpreted as all lists. Contacts on no list are never automatically granted.
A native,
read-only helper uses Apple's Contacts framework; it does not read private
Contacts databases, edit contacts, authenticate to Google/iCloud, or send an
address book to the client. Use the account and lists already populated by
your Contacts Sync/iCloud setup.

This source is disabled when the broker config has no `contacts` object.
Enabling it does not enable an intake integration, backfill history, or reset
a collection cursor. Review list selection before collecting messages.

## Build and permission setup on the Mac

Run the broker as the macOS user who owns both Messages and Contacts. The user
must have their GUI session available; use a LaunchAgent, not a root daemon.
Contacts permission is separate from the Messages backend's Full Disk Access.

Build on macOS 13 or newer with Xcode Command Line Tools:

```sh
make build
make macos-contacts macos-test
```

`make macos-contacts CODESIGN_IDENTITY="YOUR SIGNING IDENTITY"` uses an explicit
signing identity. The default is ad-hoc signing for local installation; an
updated ad-hoc build may require granting permission again. Keep the bundle
identifier and installed path stable. Synthetic tests do not request permission
or read your contacts.

Install the entire app bundle in a protected location, not just its executable.
For a first installation (stop and review before replacing an existing app):

```sh
sudo install -d -o root -g wheel -m 0755 /opt/safe-imsg/contacts
sudo ditto "bin/Safe Imsg Contacts.app" "/opt/safe-imsg/contacts/Safe Imsg Contacts.app"
sudo chown -R root:wheel "/opt/safe-imsg/contacts/Safe Imsg Contacts.app"
sudo chmod -R go-w "/opt/safe-imsg/contacts/Safe Imsg Contacts.app"
```

The executable, Info.plist and every parent must be owned by the broker user or root and
not group/other-writable. `/Applications` is commonly group-writable and does
not satisfy this rule. The configured executable must not be a symlink.
Protect the entire bundle, including its Info.plist and signature.

In the owner's Mac desktop session, open the app and allow Contacts access:

```sh
open "/opt/safe-imsg/contacts/Safe Imsg Contacts.app"
```

This explicit action requests permission; background reads never prompt.
Full access is required where macOS offers limited access: partial visibility
cannot establish complete list membership. The app does not enable a whitelist
or collection. Confirm that the desired lists and members have finished syncing
in Contacts before proceeding. If permission is denied, enable the app under
System Settings → Privacy & Security → Contacts and reopen it.

## Discover and preview, without activation

Run these owner-local commands as the Contacts owner, never as the restricted
client. Discovery prints account containers and list names/IDs, not contact
cards:

```sh
safe-imsgd contacts groups \
  --helper "/opt/safe-imsg/contacts/Safe Imsg Contacts.app/Contents/MacOS/safe-imsg-contacts"
```

Choose the iCloud container from this Mac. Names are for
display only: a same-named list in a work account must not substitute for an
iCloud list. `carddav` alone does not prove that a container is iCloud. There is
no automatic selection of unlisted contacts or other accounts. By default,
new lists in the selected container are included on the next refresh. To limit
access to particular lists instead, configure their exact IDs.

Create a separate owner-only draft of the broker config. Add this object using
the account ID you reviewed (the example ID below is a placeholder):

```json
"contacts": {
  "helper_path": "/opt/safe-imsg/contacts/Safe Imsg Contacts.app/Contents/MacOS/safe-imsg-contacts",
  "container_id": "EXACT_ICLOUD_CONTAINER_ID",
  "default_phone_region": "US",
  "refresh_seconds": 900,
  "max_age_seconds": 3600,
  "timeout_ms": 10000,
  "max_contacts": 1000
}
```

Only `helper_path` and `container_id` are required. To opt into a narrower
selection, add `"group_ids": ["EXACT_FRIENDS_LIST_ID", "EXACT_FAMILY_LIST_ID"]`.
The numeric
settings above are the defaults. The phone region has no default: omit it to
accept only international `+` numbers, or explicitly choose `US`/`CA` to also
normalize national NANP numbers. Extensions and ambiguous/invalid numbers
are skipped, not guessed. Email addresses are exact and case-folded. Every
valid phone/email field on a selected card can grant a DM; field labels such
as “work” or “home” do not restrict this.

```sh
safe-imsgd contacts preview --config /Users/owner/.safe-imsg/broker.contacts-draft.json
safe-imsgd contacts preview --config /Users/owner/.safe-imsg/broker.contacts-draft.json --show-identities
```

Preview reports unique contact count, effective direct identity count, skipped
field count, actual list IDs, `all_lists`, and `activated: false`.
`--show-identities` explicitly prints the
effective phone/email grants locally; treat that output as private. No preview
command writes configuration, changes Contacts, starts the broker, or reads
Messages. Counts need not match: one contact can supply several identities,
and several contacts can share one identity.

The effective DM grants are the union of selected Contacts fields and the
static policy's `allowed_direct`. Owner aliases never grant access. Exact
`excluded_conversations` still override every grant. Removing a contact will
not revoke an identity that remains in another selected card/list or the static
allowlist. Verified group conversations retain their independent existing rule;
this is a DM whitelist, not a restriction on group participants.

Once the owner approves the preview, put that selection in the live config,
validate it, and restart the broker. Config validation checks configuration,
trusted paths and backend startup, but does not prove Contacts permission or
list availability. After restart, check `safe-imsg ... info` for
`contacts_policy.state: "ready"` and verify an intended DM through the broker
before enabling collection. Test in the actual LaunchAgent context too, since
an interactive permission check alone is not an end-to-end daemon test.

## Refresh, removals, and failure behavior

The broker discovers and reads the selected container's lists on startup and
every 15 minutes by default. An explicit list-ID selection narrows this.
It deduplicates contacts/identities and replaces the derived grants atomically.
It fetches non-unified cards to avoid importing fields from linked accounts.
The local store revision must stay unchanged during a read. A complete empty
list is valid and removes its grants. With the all-lists default, a container
with no lists is also a valid empty snapshot; adding/removing/recreating lists
is handled automatically. A missing explicitly selected list is an error.
A missing/recreated account always requires explicit re-selection.

Normal membership changes take effect on the next successful refresh, plus any
upstream sync delay. An owner can restart the broker to refresh sooner. There
is no client-triggered refresh RPC. Static policy-file edits still apply on the
next data request without a restart.

Transient failures (unavailable store, concurrent store change, or timeout)
retain the last complete snapshot only until its one-hour default expiry.
Observed permission loss, missing selection, unsafe helper, malformed output,
or overflow invalidates it immediately. These conditions are observed at a
refresh, not continuously. With no usable snapshot, **all data reads**, including
group reads and collection, return `policy_unavailable`. Collection returns no
replacement cursor. This prevents an unavailable policy from silently skipping
DMs. Ping and sanitized source health remain available while the broker runs.

Only the in-memory normalized grants are retained between refreshes; there is
no persisted address book or last-good cache reused across restarts. Once a
complete snapshot is available, reads recover automatically. A removed grant
cannot retract messages already disclosed. Newly admitted contacts do not
automatically replay messages before an existing cursor.

Expiry measures a successful read of the **local** Contacts store, not freshness
of Google → Contacts Sync → iCloud → Mac. The framework cannot prove that this
upstream chain is current. A locally readable but unsynced store can remain
`ready`; preview therefore reports `upstream_sync_freshness: "unknown"`.
Monitor Contacts Sync/iCloud separately, especially for urgent removals. For an
urgent conversation revocation, use the static exact conversation exclusion.

## Health and testing

`system.info` adds `contacts_policy` only when configured. It reports
`ready`, `degraded` (using a still-unexpired snapshot after a transient failure),
or `unavailable`, with attempt/success/expiry timestamps and a fixed last-error
code. It exposes no container/list IDs, list names, contact counts, or grants.
There are no Contacts or policy-management methods on the client socket.

The helper is launched through macOS Launch Services so permission belongs to
the approved app, not SSH or the Go daemon. It connects to a fresh Unix socket
in an owner-only directory; both ends verify the peer's OS user. Only one
bounded, length-prefixed request/response is exchanged, entirely in memory.
Disconnect cancels the native helper; a separate 65-second watchdog bounds its
lifetime even if launch or communication stalls. No additional persistent
service, broad SSH Contacts permission, or on-disk contact snapshot is needed.
Direct `--stdio` execution remains a diagnostic interface, not the production
transport, because its TCC identity can depend on the launching process.

Bounds: at most 100 lists in one container, 1–10,000 maximum unique contacts
(default 1,000), 4 MiB helper output, and 1–60 second helper timeout. Exceeding
a bound is an error, not a truncated successful policy. The helper enumerates
only selected lists' members; discovery enumerates list metadata only.

`make ci` covers Go tests, race detection, vet, and Linux/macOS Go builds.
`make macos-test` exercises the native selection/deduplication/revision logic
using synthetic contacts. Real Contacts permissions, local account contents,
upstream sync and LaunchAgent execution still require the owner-side checks
above; synthetic tests do not establish those facts.

On macOS, setting `SAFE_IMSG_CONTACTS_APP_EXECUTABLE` to the built helper while
running the Go Contacts tests additionally exercises the real desktop app
transport and disconnect cancellation. These tests send invalid/no requests
and do not read personal Contacts data or request permission.
