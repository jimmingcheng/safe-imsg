# Threat model

## Protected assets

The broker is intended to protect conversations outside the owner's explicit
DM policy, excluded conversations, attachment data and paths, rich Messages
payloads, likely authentication codes/sign-in links, the Messages database
path/contents, and all mutating Messages capabilities.

## Trusted components

The owner OS account, operating-system kernel, pinned `safe-imsgd` and `imsg`
binaries, owner configuration and policy, and Apple's Messages database are
trusted. The client user, its processes, its prompts, and any LLM consuming
admitted output are untrusted.

With the optional Contacts policy source, the native helper/app bundle, the
owner's selected local Contacts cards/list membership, and the services/apps
that sync those lists are also trusted policy inputs. Someone able to change
a selected card's phone/email fields can change derived DM grants. The default
includes every list, including new lists, only in the exact owner-selected
container. Explicit list IDs can narrow this. Linked unified cards do not import
fields from other accounts. The client cannot discover or select contact lists
over RPC. Local-store freshness does not prove upstream cloud sync freshness.

The boundary uses filesystem ownership plus kernel-reported peer UID. Policy is
evaluated only in the trusted broker. Conversation authorization requires
consistent native group/direct metadata and exact normalized identities.
Explicit conversation exclusions override every grant. There is no generic RPC
forwarding or database selector.

## Defenses

- strict, bounded, length-prefixed request/response schemas;
- one authenticated client UID and one configured account/database per daemon;
- policy reload and authorization immediately before each data response;
- database-generation binding and live device/inode replacement detection;
- bounded backend output, result scans, text size, timeouts, and collection;
- one deadline for each request and cancellation/reaping of backend processes;
- conservative local code/link suppression before serialization;
- allowlisted outbound fields and sanitized errors with backend stderr dropped;
- safe socket lock, stale-socket probe, symlink/non-socket refusal, and
  identity-checked cleanup;
- validation of opened policy/config descriptors and trusted parent paths;
- unsupported peer-credential platforms fail closed.

## Residual risks

Secret detection is deterministic defense in depth, not a guarantee. A novel
code format, secret embedded in ordinary prose, or a non-authentication secret
can be admitted. Authorized messages and participants are visible to the
client. Row IDs and ordering can reveal gaps or relative activity. Group access
intentionally includes non-allowlisted participants once native group metadata
is verified.

New cursors are AES-GCM authenticated/encrypted with durable owner-only key
material and broker-instance associated data. They hide filtered-row positions
and the database-wide upper boundary; legacy readable v1 cursors are accepted
as migration inputs. Admitted row IDs, timing, pagination and range completion
still reveal some relative activity. Device/inode binding detects file replacement but not
an in-place database restore; the owner must rotate `database_generation` for
such a reset.

The trusted `imsg` process can read the full database and its upstream parser
is in the trusted computing base. A compromised owner account, root, kernel,
broker/backend binary, policy, service definition, or database defeats the
boundary. Existing unrestricted maintenance SSH or the ability to execute as
the owner remains a complete bypass. Unix socket filtering alone does not fix
that deployment condition.

Local denial of service remains possible through repeated permitted requests.
Bounds limit per-request cost but v1 has no rate limiter. The client can discard
or forge its own cursor and thereby omit messages from its own view; cursors do
not grant access and cannot broaden policy.

Insertion cursors do not capture edits/deletions behind a checkpoint, incomplete
cloud synchronization or new policy grants over already-scanned history.
