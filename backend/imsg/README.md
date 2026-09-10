# Pinned bounded-collection backend

This is a small source overlay for imsg 0.13.1 at
`6918867c6439298103df592d09835fdfda51a090`, not a fork of its entire source tree.
`collection.patch` registers one read-only command and identifies the build as
`0.13.1-safe-imsg.4`. New files extend its existing `IMsgCore.MessageStore`.
The dependency lock is checked in so builds do not float to later versions.
SQLite.swift uses the system SQLite default on macOS; its optional CSQLite and
SQLCipher packages appear in the resolution graph but are not collection targets.

On macOS 14+ with Swift 6.1 or newer and an Apple SDK:

```sh
sh backend/imsg/build.sh /absolute/new/build-directory
```

The destination must not exist. To use an isolated compiler, set
`SAFE_IMSG_SWIFT=/absolute/toolchain/usr/bin/swift`. The script checks out the
exact upstream revision, applies the overlay, installs the lock, runs synthetic
collection tests, and builds the release executable with two jobs. It does not
install anything, read the owner's Messages, or run upstream live messaging tests.

Install the resulting executable **and its `.bundle` resource directories** in
a new root-owned release directory. Verify its version/signature and configure
`backend_path` and `backend_version` together. Keep the previous backend/broker
for rollback. Full Disk Access must work in the production LaunchAgent context;
an owner SSH test alone is insufficient. No Contacts app rebuild is needed.

See [the backend contract](../../docs/backend-contract.md) for the narrow fields,
resource bounds, checkpoint protocol and insertion-only completeness guarantee.
The optional `--not-before` bootstrap uses a required leading date index to page
only plausible ordinary messages for the configured account inside a fixed
snapshot. It excludes reactions, app payloads, other accounts, empty events,
and implausible future dates. Its opaque cursor retains the timestamp between
pages and advances to the snapshot boundary when complete; later collection
uses insertion order so late sync arrivals are not missed. Consumers also
retain and enforce the timestamp as defense in depth.
