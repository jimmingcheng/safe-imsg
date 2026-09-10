#!/bin/sh
# Build in a NEW owner-selected directory. Never patch an existing checkout.
set -eu
test "$(uname -s)" = Darwin
test "$#" = 1 || { echo 'usage: build.sh NEW_BUILD_DIRECTORY' >&2; exit 2; }
test ! -e "$1" || { echo 'Build directory already exists; refusing to overwrite it.' >&2; exit 2; }
overlay_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
: "${SAFE_IMSG_SWIFT:=swift}"
git clone https://github.com/openclaw/imsg.git "$1"
cd "$1"
git checkout --detach 6918867c6439298103df592d09835fdfda51a090
git apply --check "$overlay_dir/collection.patch"
git apply "$overlay_dir/collection.patch"
install -m 0644 "$overlay_dir/MessageStore+SafeCollection.swift" Sources/IMsgCore/
install -m 0644 "$overlay_dir/CollectCommand.swift" Sources/imsg/Commands/
install -m 0644 "$overlay_dir/SafeCollectionTests.swift" Tests/IMsgCoreTests/
install -m 0644 "$overlay_dir/Package.resolved" Package.resolved
"$SAFE_IMSG_SWIFT" test --force-resolved-versions --filter SafeCollection --jobs 2
"$SAFE_IMSG_SWIFT" build --force-resolved-versions -c release --product imsg --jobs 2
test "$(.build/release/imsg --version)" = 0.13.1-safe-imsg.3
echo 'Build and synthetic collection tests passed. No production installation performed.'
