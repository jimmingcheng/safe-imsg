import Foundation

enum ContactsFailure: String, Error {
    case unavailable = "contacts_unavailable"
    case permissionRequired = "contacts_permission_required"
    case permissionDenied = "contacts_permission_denied"
    case selectionMissing = "contacts_selection_missing"
    case overflow = "contacts_overflow"
    case storeChanged = "contacts_store_changed"
    case invalid = "contacts_invalid_response"
}

struct ContactRecord: Codable, Equatable {
    let id: String
    let phones: [String]
    let emails: [String]
}

struct GroupRecord: Codable {
    let id: String
    let name: String
}

struct ContainerRecord: Codable {
    let id: String
    let name: String
    let type: String
}

struct DiscoveryContainer: Codable {
    let id: String
    let name: String
    let type: String
    let groups: [GroupRecord]
}

struct DiscoveryResult: Codable {
    let containers: [DiscoveryContainer]
}

struct ContactsSnapshot: Codable {
    let container_id: String
    let group_ids: [String]
    let contacts: [ContactRecord]
    let complete: Bool
}

protocol ContactSource {
    func revision() throws -> Data
    func containers() throws -> [ContainerRecord]
    func groups(containerID: String) throws -> [GroupRecord]
    func contacts(groupID: String, limit: Int) throws -> [ContactRecord]
}

func boundedString(_ value: String) -> Bool {
    !value.isEmpty && value.utf8.count <= 512 && !value.contains("\0")
}

func discover(_ source: ContactSource) throws -> DiscoveryResult {
    let before = try source.revision()
    let containers = try source.containers()
    guard containers.count <= 100, Set(containers.map(\.id)).count == containers.count else {
        throw ContactsFailure.overflow
    }
    let result = try containers.map { container -> DiscoveryContainer in
        guard boundedString(container.id), container.name.utf8.count <= 512 else { throw ContactsFailure.invalid }
        let groups = try source.groups(containerID: container.id)
        guard groups.count <= 1000, Set(groups.map(\.id)).count == groups.count else { throw ContactsFailure.overflow }
        for group in groups {
            guard boundedString(group.id), group.name.utf8.count <= 512 else { throw ContactsFailure.invalid }
        }
        return DiscoveryContainer(id: container.id, name: container.name, type: container.type,
                                  groups: groups.sorted { $0.id < $1.id })
    }
    guard try source.revision() == before else { throw ContactsFailure.storeChanged }
    return DiscoveryResult(containers: result.sorted { $0.id < $1.id })
}

func snapshot(_ source: ContactSource, containerID: String, groupIDs: [String]?, limit: Int) throws -> ContactsSnapshot {
    guard boundedString(containerID), limit > 0, limit <= 10000 else { throw ContactsFailure.invalid }
    if let groupIDs {
        guard !groupIDs.isEmpty, groupIDs.count <= 100, groupIDs.allSatisfy(boundedString),
              Set(groupIDs).count == groupIDs.count else { throw ContactsFailure.invalid }
    }
    let before = try source.revision()
    let containers = try source.containers()
    guard containers.count <= 100 else { throw ContactsFailure.overflow }
    guard containers.filter({ $0.id == containerID }).count == 1 else { throw ContactsFailure.selectionMissing }
    let groups = try source.groups(containerID: containerID)
    guard groups.count <= 1000 else { throw ContactsFailure.overflow }
    // nil selects the union of actual lists, never every card in the account.
    let selectedIDs = groupIDs ?? groups.map(\.id)
    guard selectedIDs.count <= 100 else { throw ContactsFailure.overflow }
    guard selectedIDs.allSatisfy(boundedString) else { throw ContactsFailure.invalid }
    let selected = Set(selectedIDs)
    guard Set(groups.map(\.id)).count == groups.count else { throw ContactsFailure.invalid }
    guard selected.isSubset(of: Set(groups.map(\.id))) else { throw ContactsFailure.selectionMissing }
    var records: [String: ContactRecord] = [:]
    for groupID in selectedIDs.sorted() {
        let members = try source.contacts(groupID: groupID, limit: limit)
        guard members.count <= limit else { throw ContactsFailure.overflow }
        for contact in members {
            guard boundedString(contact.id), contact.phones.count <= 100, contact.emails.count <= 100,
                  (contact.phones + contact.emails).allSatisfy({ $0.utf8.count <= 512 }) else {
                throw ContactsFailure.overflow
            }
            if let prior = records[contact.id], prior != contact { throw ContactsFailure.storeChanged }
            records[contact.id] = contact
            guard records.count <= limit else { throw ContactsFailure.overflow }
        }
    }
    guard try source.revision() == before else { throw ContactsFailure.storeChanged }
    return ContactsSnapshot(container_id: containerID, group_ids: selectedIDs.sorted(),
                            contacts: records.values.sorted { $0.id < $1.id }, complete: true)
}
