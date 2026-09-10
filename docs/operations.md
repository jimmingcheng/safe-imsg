# Operator guide

## Principals and files

Choose a trusted macOS Messages owner and a distinct restricted client user.
The restricted user must not have sudo, owner SSH credentials, Full Disk Access
to the owner's Messages data, or write access to the broker binary/config,
policy, backend, database, socket directory, or service definition.

Example setup (replace names and IDs):

```sh
sudo dseditgroup -o create safe-imsg
sudo dseditgroup -o edit -a messages-owner -t user safe-imsg
sudo dseditgroup -o edit -a agent-user -t user safe-imsg
sudo install -d -o messages-owner -g safe-imsg -m 0750 /Users/Shared/safe-imsg
```

Install both Go binaries somewhere root-owned. Install the audited `imsg`
binary at a stable, non-symlink path. The owner should keep broker configuration
and policy mode `0600` in an owner-only directory. `chat.db` is opened by
the configured `imsg` child for content; safe-imsg validates file permissions
and identity without querying SQLite. Paths must be absolute and clean (no `..`
components). All parent directories must be owned by
the broker user or root and protected from group/other writes. Root-owned
sticky directories are allowed as ancestors. Trusted ancestor symlinks such as
macOS `/var` are checked along with their resolved targets.

Set the socket mode to `0660` when the owner and client share the socket group.
The immediate socket directory must be owner-owned and not writable by group or
other. `safe-imsgd` refuses to replace symlinks, regular files, or active Unix
sockets. It removes only a connection-refused stale socket while holding an
owner-only adjacent lock, and removes its own socket at shutdown only if the
file identity still matches. Existing lock files must have one link and private
permissions; lock validation never changes an existing file's permissions.

## Validation and startup

```sh
chmod 600 /Users/messages-owner/.config/safe-imsg/*.json
safe-imsgd config validate --config /Users/messages-owner/.config/safe-imsg/broker.json
safe-imsgd run --config /Users/messages-owner/.config/safe-imsg/broker.json
```

Production startup should use a `launchd` user agent running as the Messages
owner, with an absolute `ProgramArguments` array containing `safe-imsgd`,
`run`, `--config`, and the config path. Do not run the broker as root.
Use `ProcessType` `Standard` for this request-serving agent: `Background`
subjects backend children to additional CPU/I/O throttling even while a client
is waiting. Keep a finite `backend_timeout_ms` (default 10000, maximum 60000);
30000 can provide headroom for cold reads or bounded GUID lookups on older
Macs. Client deadlines must exceed the broker budget plus transport overhead.

After policy changes, no restart is needed: every data response loads policy
again. Invalid or unsafe policy makes data operations fail closed. After a
database reset, stop the broker, change `database_generation`, validate, and
restart. Never reuse an old cursor across that change.

For automatic DM grants from the owner's local Contacts lists, follow
[Contacts-backed policy setup](contacts-policy.md). Membership refreshes do not
require a restart; changes to the configured container/list selection do. This
optional source requires its own native app and Contacts permission.

## Monitoring and audit

The daemon does not log message bodies, participants, GUIDs, backend stderr, or
raw backend failures. Supervise process health and `system.ping`; treat a
`backend_unavailable`, `backend_timeout`, `backend_invalid`, database identity change, or
`collection_overflow` as an operator event. Resolve the cause rather than
raising scan bounds without reviewing resource impact.

`collection_incomplete` is retryable and leaves the cursor unchanged. It means
watch produced no observable rows before its window ended, which can reflect a
quiet database, slow startup, or a batch suppressed internally by upstream.
Repeated lack of progress requires owner investigation; never advance a cursor
manually to bypass an unresolved batch if complete collection is required.

No live message read, export, send, OpenClaw modification, SSH change, or Donna
bridge change is part of repository installation or testing.
