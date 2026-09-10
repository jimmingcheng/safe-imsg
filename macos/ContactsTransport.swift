import Darwin
import Foundation

// A Launch Services-launched app has its own TCC identity. Its only data channel
// is a one-request socket inside a freshly created owner-private directory.
func connectOwnerChannel(_ path: String) throws {
    guard path.hasPrefix("/"), !path.contains("\0"),
          URL(fileURLWithPath: path).standardizedFileURL.path == path else { throw ContactsFailure.invalid }
    var address = sockaddr_un()
    let bytes = Array(path.utf8CString)
    guard bytes.count <= MemoryLayout.size(ofValue: address.sun_path) else { throw ContactsFailure.invalid }
    let parent = URL(fileURLWithPath: path).deletingLastPathComponent().path
    var directory = stat()
    var target = stat()
    guard lstat(parent, &directory) == 0, directory.st_uid == geteuid(),
          directory.st_mode & S_IFMT == S_IFDIR, directory.st_mode & 0o077 == 0,
          lstat(path, &target) == 0, target.st_uid == geteuid(),
          target.st_mode & S_IFMT == S_IFSOCK else { throw ContactsFailure.unavailable }
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { throw ContactsFailure.unavailable }
    defer { close(fd) }
    address.sun_family = sa_family_t(AF_UNIX)
    address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
    withUnsafeMutableBytes(of: &address.sun_path) { destination in
        bytes.withUnsafeBytes { destination.copyBytes(from: $0) }
    }
    let connected = withUnsafePointer(to: &address) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
            connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
        }
    }
    guard connected == 0 else { throw ContactsFailure.unavailable }
    var owner: uid_t = 0
    var group: gid_t = 0
    guard getpeereid(fd, &owner, &group) == 0, owner == geteuid(),
          dup2(fd, STDIN_FILENO) >= 0, dup2(fd, STDOUT_FILENO) >= 0 else { throw ContactsFailure.unavailable }
}

func watchOwnerDisconnect() {
    // Closing the broker channel cancels even a stalled Contacts framework call.
    // No PID lookup/kill-by-name and no long-lived GUI background service.
    DispatchQueue.global(qos: .utility).async {
        var event = pollfd(fd: STDIN_FILENO, events: 0, revents: 0)
        while true {
            let count = poll(&event, 1, -1)
            if count > 0 && event.revents & Int16(POLLHUP | POLLERR | POLLNVAL) != 0 { exit(1) }
            if count < 0 && errno != EINTR { exit(1) }
        }
    }
}

func readExact(_ handle: FileHandle, count: Int) throws -> Data {
    var result = Data()
    while result.count < count {
        guard let chunk = try handle.read(upToCount: count - result.count), !chunk.isEmpty else {
            throw ContactsFailure.invalid
        }
        result.append(chunk)
    }
    return result
}

func readContactFrame(_ handle: FileHandle, maximum: Int) throws -> Data {
    let header = try readExact(handle, count: 4)
    let length = header.reduce(0) { ($0 << 8) | Int($1) }
    guard length > 0, length <= maximum else { throw ContactsFailure.overflow }
    return try readExact(handle, count: length)
}

func writeContactFrame(_ handle: FileHandle, data: Data) throws {
    var length = UInt32(data.count).bigEndian
    try withUnsafeBytes(of: &length) { try handle.write(contentsOf: Data($0)) }
    try handle.write(contentsOf: data)
}
