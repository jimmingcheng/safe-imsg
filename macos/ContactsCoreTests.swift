import Foundation

final class Fixture: ContactSource {
    var token = Data([1])
    var changeDuringRead = false
    var calls: [String] = []
    var groupIDs = ["friends", "family"]
    var members = ["friends": [ContactRecord(id: "c1", phones: ["+14155550123"], emails: [])],
                   "family": [ContactRecord(id: "c1", phones: ["+14155550123"], emails: [])]]
    func revision() throws -> Data { token }
    func containers() throws -> [ContainerRecord] {
        [ContainerRecord(id: "icloud", name: "iCloud", type: "carddav"),
         ContainerRecord(id: "work", name: "Work", type: "exchange")]
    }
    func groups(containerID: String) throws -> [GroupRecord] {
        containerID == "icloud" ? groupIDs.map { GroupRecord(id: $0, name: $0) }
            : [GroupRecord(id: "work-friends", name: "Friends")]
    }
    func contacts(groupID: String, limit: Int) throws -> [ContactRecord] {
        calls.append(groupID)
        if changeDuringRead { token = Data([2]) }
        return members[groupID] ?? []
    }
}

func expectFailure(_ expected: ContactsFailure, _ action: () throws -> Void) {
    do { try action(); fatalError("expected \(expected)") }
    catch let error as ContactsFailure { precondition(error == expected, "unexpected \(error)") }
    catch { fatalError("unexpected error") }
}

@main struct ContactsCoreTests {
    static func main() throws {
        let source = Fixture()
        let result = try snapshot(source, containerID: "icloud", groupIDs: ["friends", "family"], limit: 10)
        precondition(result.contacts.count == 1 && result.complete)
        precondition(!source.calls.contains("work-friends"))
        let discovered = try discover(source)
        precondition(discovered.containers.count == 2)
        expectFailure(.selectionMissing) { _ = try snapshot(source, containerID: "missing", groupIDs: ["friends"], limit: 10) }
        expectFailure(.selectionMissing) { _ = try snapshot(source, containerID: "icloud", groupIDs: ["work-friends"], limit: 10) }
        expectFailure(.invalid) { _ = try snapshot(source, containerID: "icloud", groupIDs: [], limit: 10) }
        expectFailure(.invalid) { _ = try snapshot(source, containerID: "icloud", groupIDs: ["friends", "friends"], limit: 10) }
        source.changeDuringRead = true
        expectFailure(.storeChanged) { _ = try snapshot(source, containerID: "icloud", groupIDs: ["friends"], limit: 10) }
        source.changeDuringRead = false
        source.members["family"] = [ContactRecord(id: "c1", phones: ["+14155550999"], emails: [])]
        expectFailure(.storeChanged) { _ = try snapshot(source, containerID: "icloud", groupIDs: ["family", "friends"], limit: 10) }
        source.members["family"] = [ContactRecord(id: "c2", phones: [], emails: [])]
        expectFailure(.overflow) { _ = try snapshot(source, containerID: "icloud", groupIDs: ["family", "friends"], limit: 1) }
        source.members["friends"] = []
        let empty = try snapshot(source, containerID: "icloud", groupIDs: ["friends"], limit: 1)
        precondition(empty.complete && empty.contacts.isEmpty)
        source.members["new-list"] = [ContactRecord(id: "new", phones: [], emails: ["new@example.test"])]
        source.members["unlisted"] = [ContactRecord(id: "unlisted", phones: [], emails: ["unlisted@example.test"])]
        source.groupIDs.append("new-list")
        let all = try snapshot(source, containerID: "icloud", groupIDs: nil, limit: 10)
        precondition(all.contacts.count == 2 && all.group_ids.contains("new-list"))
        precondition(!source.calls.contains("unlisted") && !source.calls.contains("work-friends"))
        source.groupIDs = []
        let noLists = try snapshot(source, containerID: "icloud", groupIDs: nil, limit: 10)
        precondition(noLists.complete && noLists.contacts.isEmpty && noLists.group_ids.isEmpty)
        expectFailure(.selectionMissing) { _ = try snapshot(source, containerID: "missing", groupIDs: nil, limit: 10) }
        print("Contacts core tests passed")
    }
}
