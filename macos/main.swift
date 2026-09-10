import AppKit
import Contacts
import Foundation

var socketTransport = false

// Only this native boundary accesses Contacts. There are no save requests,
// AppleScript, raw address-book database reads, or iCloud network credentials.
final class NativeContacts: ContactSource {
    private let store = CNContactStore()

    init() throws { try checkPermission() }

    private func checkPermission() throws {
        switch CNContactStore.authorizationStatus(for: .contacts) {
        case .authorized: return
        case .notDetermined: throw ContactsFailure.permissionRequired
        default: throw ContactsFailure.permissionDenied
        }
    }

    func revision() throws -> Data {
        try checkPermission()
        guard let token = store.currentHistoryToken else { throw ContactsFailure.unavailable }
        return token
    }

    func containers() throws -> [ContainerRecord] {
        try checkPermission()
        return try store.containers(matching: nil).map {
            let type: String
            switch $0.type {
            case .local: type = "local"
            case .exchange: type = "exchange"
            case .cardDAV: type = "carddav"
            default: type = "unknown"
            }
            return ContainerRecord(id: $0.identifier, name: $0.name, type: type)
        }
    }

    func groups(containerID: String) throws -> [GroupRecord] {
        try checkPermission()
        return try store.groups(matching: CNGroup.predicateForGroupsInContainer(withIdentifier: containerID))
            .map { GroupRecord(id: $0.identifier, name: $0.name) }
    }

    func contacts(groupID: String, limit: Int) throws -> [ContactRecord] {
        try checkPermission()
        let request = CNContactFetchRequest(keysToFetch: [CNContactIdentifierKey, CNContactPhoneNumbersKey,
                                                        CNContactEmailAddressesKey] as [CNKeyDescriptor])
        request.predicate = CNContact.predicateForContactsInGroup(withIdentifier: groupID)
        // Unified contacts may merge phone/email fields from linked accounts.
        // A selected iCloud group must not grant access via a linked work card.
        request.unifyResults = false
        var result: [ContactRecord] = []
        var overflow = false
        try store.enumerateContacts(with: request) { contact, stop in
            if result.count >= limit { overflow = true; stop.pointee = true; return }
            result.append(ContactRecord(id: contact.identifier,
                phones: contact.phoneNumbers.map { $0.value.stringValue }.sorted(),
                emails: contact.emailAddresses.map { $0.value as String }.sorted()))
        }
        if overflow { throw ContactsFailure.overflow }
        return result
    }
}

struct HelperRequest: Decodable {
    let v: Int
    let operation: String
    let container_id: String?
    let group_ids: [String]?
    let max_contacts: Int?
}

struct Success<T: Encodable>: Encodable {
    let v = 1
    let ok = true
    let result: T
}

struct Failure: Encodable {
    let v = 1
    let ok = false
    let code: String
}

func emit<T: Encodable>(_ response: T) throws {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.sortedKeys]
    let data = try encoder.encode(response)
    guard data.count <= 4 * 1024 * 1024 else { throw ContactsFailure.overflow }
    if socketTransport {
        try writeContactFrame(.standardOutput, data: data)
    } else {
        try FileHandle.standardOutput.write(contentsOf: data)
        try FileHandle.standardOutput.write(contentsOf: Data([10]))
    }
}

func runRequest() throws {
    // Bounded input; only the trusted owner-side broker invokes this helper.
    var data = Data()
    if socketTransport {
        data = try readContactFrame(.standardInput, maximum: 64 * 1024)
    } else {
        while let chunk = try FileHandle.standardInput.read(upToCount: 4096), !chunk.isEmpty {
            data.append(chunk)
            guard data.count <= 64 * 1024 else { throw ContactsFailure.invalid }
        }
    }
    guard let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw ContactsFailure.invalid }
    let request = try JSONDecoder().decode(HelperRequest.self, from: data)
    guard request.v == 1 else { throw ContactsFailure.invalid }
    let allowed: Set<String> = request.operation == "snapshot"
        ? ["v", "operation", "container_id", "group_ids", "max_contacts"] : ["v", "operation"]
    guard Set(object.keys) == allowed else { throw ContactsFailure.invalid }
    guard request.operation == "discover" || request.operation == "snapshot" else { throw ContactsFailure.invalid }
    let source = try NativeContacts()
    if request.operation == "discover" {
        try emit(Success(result: discover(source)))
    } else {
        guard let container = request.container_id, let limit = request.max_contacts else { throw ContactsFailure.invalid }
        try emit(Success(result: snapshot(source, containerID: container, groupIDs: request.group_ids, limit: limit)))
    }
}

func authorizeInteractively() -> Never {
    // An explicit owner action only. Daemon/CLI reads never trigger a prompt.
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)
    let store = CNContactStore()
    store.requestAccess(for: .contacts) { _, _ in
        DispatchQueue.main.async {
            let granted = CNContactStore.authorizationStatus(for: .contacts) == .authorized
            let alert = NSAlert()
            alert.messageText = granted ? "Contacts access is ready" : "Contacts access was not granted"
            alert.informativeText = granted
                ? "Safe Imsg can now preview selected contact lists. This has not enabled a whitelist or message collection."
                : "Allow Safe Imsg Contacts full access in System Settings → Privacy & Security → Contacts, then reopen this app."
            alert.addButton(withTitle: "OK")
            app.activate(ignoringOtherApps: true)
            alert.runModal()
            exit(granted ? 0 : 1)
        }
    }
    app.run()
    exit(1)
}

let arguments = Array(CommandLine.arguments.dropFirst())
if arguments.isEmpty || arguments == ["authorize"] {
    authorizeInteractively()
}
let connected = arguments.count == 2 && arguments[0] == "--connect"
if arguments != ["--stdio"] && !connected {
    try? emit(Failure(code: ContactsFailure.invalid.rawValue))
    exit(2)
}
do {
    if connected {
        // Absolute lifetime cap, including a stalled desktop launch/client.
        signal(SIGALRM, SIG_DFL)
        alarm(65)
        signal(SIGPIPE, SIG_IGN)
        try connectOwnerChannel(arguments[1])
        socketTransport = true
        watchOwnerDisconnect()
    }
    try runRequest()
} catch let failure as ContactsFailure {
    try? emit(Failure(code: failure.rawValue))
    exit(1)
} catch {
    // Framework exceptions and backend strings may contain private data.
    // Permission can be revoked between a status check and a framework call.
    // Do not treat that race as a transient error that retains cached grants.
    let status = CNContactStore.authorizationStatus(for: .contacts)
    let failure: ContactsFailure = status == .authorized ? .unavailable
        : (status == .notDetermined ? .permissionRequired : .permissionDenied)
    try? emit(Failure(code: failure.rawValue))
    exit(1)
}
