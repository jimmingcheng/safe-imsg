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
- conservative local code/link suppression before serialization;
- allowlisted outbound fields and sanitized errors with backend stderr dropped;
- safe socket lock, stale-socket probe, symlink/non-socket refusal, and
  identity-checked cleanup;
- unsupported peer-credential platforms fail closed.

## Residual risks

Secret detection is deterministic defense in depth, not a guarantee. A novel
code format, secret embedded in ordinary prose, or a non-authentication secret
can be admitted. Authorized messages and participants are visible to the
client. Row IDs and ordering can reveal gaps or relative activity. Group access
intentionally includes non-allowlisted participants once native group metadata
is verified.

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
