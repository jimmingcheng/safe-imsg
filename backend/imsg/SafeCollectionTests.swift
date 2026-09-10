import Foundation
import SQLite
import Testing
@testable import IMsgCore

@Suite struct SafeCollectionTests {
  private func fixture(_ count: Int = 0) throws -> (MessageStore, Connection) {
    let db = try Connection(.inMemory)
    try db.execute("""
      CREATE TABLE message (ROWID INTEGER PRIMARY KEY, handle_id INTEGER, text TEXT,
        attributedBody BLOB, guid TEXT, associated_message_guid TEXT,
        associated_message_type INTEGER, balloon_bundle_id TEXT, date INTEGER,
        is_from_me INTEGER, service TEXT);
      CREATE TABLE handle (ROWID INTEGER PRIMARY KEY, id TEXT);
      CREATE TABLE chat (ROWID INTEGER PRIMARY KEY, chat_identifier TEXT, guid TEXT,
        display_name TEXT, service_name TEXT, account_id TEXT);
      CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER);
      CREATE INDEX message_chat ON chat_message_join(message_id);
      CREATE TABLE chat_handle_join (chat_id INTEGER, handle_id INTEGER);
      INSERT INTO handle VALUES (1, '+14155550100');
      INSERT INTO chat VALUES (1, '+14155550100', 'iMessage;-;+14155550100', 'PRIVATE NAME', 'iMessage', 'account');
      INSERT INTO chat_handle_join VALUES (1, 1);
      """)
    for id in 1...max(1, count) where id <= count { try insert(db, id) }
    return (try MessageStore(connection: db, path: ":memory:"), db)
  }

  private func insert(_ db: Connection, _ id: Int, kind: Int = 0) throws {
    try db.run("INSERT INTO message(ROWID, handle_id, text, guid, associated_message_type, date, is_from_me, service) VALUES (?, 1, 'ordinary text', ?, ?, 1000000000, 0, 'iMessage')", id, "message-\(id)", kind)
    try db.run("INSERT INTO chat_message_join VALUES (1, ?)", id)
  }

  @Test func largeBacklogAndFixedBoundary() throws {
    let (store, db) = try fixture(1205)
    let first = try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 500)
    #expect(first.rows.count == 500)
    #expect(first.scannedThroughRowID == 500)
    #expect(first.throughRowID == 1205)
    #expect(!first.complete)
    try insert(db, 1206)
    let second = try store.safeCollection(afterRowID: 500, throughRowID: first.throughRowID, limit: 500)
    #expect(second.rows.first?.id == 501)
    #expect(second.scannedThroughRowID == 1000)
    #expect(!second.complete)
    let last = try store.safeCollection(afterRowID: 1000, throughRowID: first.throughRowID, limit: 500)
    #expect(last.rows.count == 205)
    #expect(last.complete)
    #expect(last.scannedThroughRowID == 1205)
    let next = try store.safeCollection(afterRowID: 1205, throughRowID: nil, limit: 500)
    #expect(next.rows.map(\.id) == [1206])
    #expect(next.complete)
  }

  @Test func filteredOnlyRowsStillAdvance() throws {
    let (store, db) = try fixture()
    for id in 1...120 { try insert(db, id, kind: 2000) }
    try insert(db, 121)
    let first = try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 100)
    #expect(first.rows.count == 100)
    #expect(first.rows.allSatisfy { $0.message == nil && $0.chat == nil })
    #expect(first.scannedThroughRowID == 100)
    #expect(!first.complete)
    let second = try store.safeCollection(afterRowID: 100, throughRowID: first.throughRowID, limit: 100)
    #expect(second.rows.compactMap(\.message).count == 1)
    #expect(second.rows.last?.message?.text == "ordinary text")
    #expect(second.complete)
  }

  @Test func emptyAndExactlyFullPagesProveCompletion() throws {
    let (empty, _) = try fixture()
    let page = try empty.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10)
    #expect(page.rows.isEmpty && page.complete && page.throughRowID == 0)
    let (store, _) = try fixture(10)
    let full = try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10)
    #expect(full.rows.count == 10 && full.complete)
    let quiet = try store.safeCollection(afterRowID: 10, throughRowID: nil, limit: 10)
    #expect(quiet.rows.isEmpty && quiet.complete && quiet.scannedThroughRowID == 10)
  }

  @Test func retryAndDeletedTailDoNotLosePosition() throws {
    let (store, db) = try fixture(4)
    let first = try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 2)
    let retry = try store.safeCollection(afterRowID: 0, throughRowID: first.throughRowID, limit: 2)
    #expect(first.rows.map(\.id) == retry.rows.map(\.id))
    try db.run("DELETE FROM message WHERE ROWID > 2")
    let tail = try store.safeCollection(afterRowID: 2, throughRowID: first.throughRowID, limit: 2)
    #expect(tail.rows.isEmpty && tail.complete && tail.scannedThroughRowID == 4)
  }

  @Test func orphanAndRichRowsAreExplicitlySkipped() throws {
    let (store, db) = try fixture(3)
    try db.run("DELETE FROM chat_message_join WHERE message_id = 1")
    try db.run("UPDATE message SET balloon_bundle_id = 'com.example.poll' WHERE ROWID = 2")
    let page = try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10)
    #expect(page.rows.map(\.id) == [1, 2, 3])
    #expect(page.rows.compactMap(\.message).count == 1)
    #expect(page.rows.last?.chat?.accountID == "account")
    #expect(page.rows.last?.participants == ["+14155550100"])
    #expect(page.complete)
  }

  @Test func invalidMetadataAndBoundsFail() throws {
    let (store, db) = try fixture(1)
    for (after, through, limit) in [(Int64(-1), Int64(1), 10), (2, 1, 10), (0, 1, 0), (0, 1, 1001)] {
      #expect(throws: (any Error).self) { try store.safeCollection(afterRowID: after, throughRowID: through, limit: limit) }
    }
    try db.run("INSERT INTO chat_message_join VALUES (2, 1)")
    #expect(throws: (any Error).self) { try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10) }
    try db.run("DELETE FROM chat_message_join WHERE chat_id = 2")
    try db.run("UPDATE message SET is_from_me = NULL")
    #expect(throws: (any Error).self) { try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10) }
    try db.run("UPDATE message SET is_from_me = 0, text = ?", String(repeating: "x", count: 65537))
    #expect(throws: (any Error).self) { try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10) }
  }

  @Test func foreignAccountRowsAdvanceWithoutReadingTheirBodies() throws {
    let (store, db) = try fixture(1)
    try db.run("UPDATE message SET text = ?", String(repeating: "x", count: 65537))
    let page = try store.safeCollection(afterRowID: 0, throughRowID: nil, limit: 10, accountID: "another-account")
    #expect(page.rows.count == 1 && page.rows[0].message == nil && page.complete)
  }
}
