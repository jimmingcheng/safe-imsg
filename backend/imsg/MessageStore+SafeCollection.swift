import Foundation
import SQLite

// Overlay for the pinned imsg IMsgCore target. This is an insertion-row scan,
// not history enrichment or a change feed for edits to existing rows.
public struct SafeCollectionMessage: Sendable {
  public let guid: String
  public let sender: String
  public let fromMe: Bool
  public let text: String
  public let date: Date
}

public struct SafeCollectionRow: Sendable {
  public let id: Int64
  public let message: SafeCollectionMessage?
  public let chat: ChatInfo?
  public let participants: [String]
}

public struct SafeCollectionPage: Sendable {
  public let rows: [SafeCollectionRow]
  public let throughRowID: Int64
  public let scannedThroughRowID: Int64
  public let complete: Bool
}

public enum SafeCollectionError: Error {
  case invalidBounds, unsupportedSchema, unsafeMetadata, oversizedText
}

extension MessageStore {
  public func safeCollection(afterRowID: Int64, throughRowID: Int64?, limit: Int,
    accountID: String? = nil, notBefore: Date? = nil) throws
    -> SafeCollectionPage
  {
    guard afterRowID >= 0, limit > 0, limit <= 1000,
      throughRowID == nil || throughRowID! >= afterRowID,
      notBefore == nil || (afterRowID == 0 && throughRowID == nil)
    else { throw SafeCollectionError.invalidBounds }
    guard schema.hasReactionColumns else { throw SafeCollectionError.unsupportedSchema }

    return try withConnection { db in
      var page: SafeCollectionPage?
      // The boundary, physical row selection, messages and chat metadata all
      // come from one read-only SQLite snapshot. No long-lived snapshot is held
      // between pages. New row IDs above the boundary wait for the next cycle.
      // Keep the transaction on MessageStore's serialized queue. SQLite.swift's
      // transaction closure switches to its own queue, which would deadlock
      // when chatInfo/participants re-enter MessageStore's queue.
      try db.execute("BEGIN DEFERRED TRANSACTION")
      do {
        let upper = try throughRowID ?? max(afterRowID, int64Value(db.scalar("SELECT MAX(ROWID) FROM message")) ?? 0)
        var lower = afterRowID
        if let notBefore {
          // Find the earliest insertion row whose message timestamp is in the
          // requested horizon. Rows after it are still scanned physically and
          // older interleaved rows are left for the consumer's durable cutoff.
          let threshold = MessageStore.appleEpoch(notBefore)
          if let first = int64Value(try db.scalar(
            "SELECT MIN(ROWID) FROM message WHERE ROWID <= ? AND date >= ?", upper, threshold))
          {
            lower = max(0, first - 1)
          } else {
            lower = upper
          }
        }
        let ids = try db.prepareRowIterator(
          "SELECT ROWID AS id FROM message WHERE ROWID > ? AND ROWID <= ? ORDER BY ROWID LIMIT ?",
          bindings: [lower, upper, limit])
        var rawIDs: [Int64] = []
        while let row = try ids.failableNext() {
          guard let id = try int64Value(row, "id"), id > lower else {
            throw SafeCollectionError.unsafeMetadata
          }
          rawIDs.append(id)
        }
        var rows: [SafeCollectionRow] = []
        var chats: [Int64: (ChatInfo, [String])] = [:]
        for id in rawIDs {
          rows.append(try safeCollectionRow(id, db: db, chats: &chats, accountID: accountID))
        }
        let last = rawIDs.last ?? lower
        let more = int64Value(try db.scalar(
          "SELECT EXISTS(SELECT 1 FROM message WHERE ROWID > ? AND ROWID <= ? LIMIT 1)", last, upper)) == 1
        page = SafeCollectionPage(rows: rows, throughRowID: upper,
          scannedThroughRowID: more ? last : upper, complete: !more)
        try db.execute("COMMIT")
      } catch {
        try? db.execute("ROLLBACK")
        throw error
      }
      guard let page else { throw SafeCollectionError.unsafeMetadata }
      return page
    }
  }

  private func safeCollectionRow(_ id: Int64, db: Connection,
    chats: inout [Int64: (ChatInfo, [String])], accountID: String?) throws -> SafeCollectionRow
  {
    let skipped = SafeCollectionRow(id: id, message: nil, chat: nil, participants: [])
    let balloon = schema.hasBalloonBundleIDColumn ? "balloon_bundle_id" : "NULL"
    let kinds = try db.prepareRowIterator(
      "SELECT associated_message_type AS kind, \(balloon) AS balloon FROM message WHERE ROWID = ?",
      bindings: [id])
    guard let kind = try kinds.failableNext() else { throw SafeCollectionError.unsafeMetadata }
    let associated = try intValue(kind, "kind") ?? 0
    let bundle = try stringValue(kind, "balloon")
    // Reactions, polls and non-text app payloads do not become observations.
    // Their physical row positions are still included in checkpoint progress.
    if associated != 0 || (!bundle.isEmpty && !bundle.hasSuffix("com.apple.messages.URLBalloonProvider")) {
      return skipped
    }

    let links = try db.prepareRowIterator(
      "SELECT DISTINCT chat_id FROM chat_message_join WHERE message_id = ? LIMIT 2", bindings: [id])
    guard let link = try links.failableNext(), let chatID = try int64Value(link, "chat_id"), chatID > 0 else {
      // Rows without a conversation are not visible to any conversation grant.
      return skipped
    }
    guard try links.failableNext() == nil else { throw SafeCollectionError.unsafeMetadata }
    if chats[chatID] == nil {
      guard let info = try chatInfo(chatID: chatID) else { throw SafeCollectionError.unsafeMetadata }
      let handles = try participants(chatID: chatID)
      guard handles.count <= 256, handles.allSatisfy({ $0.utf8.count <= 320 }) else {
        throw SafeCollectionError.unsafeMetadata
      }
      chats[chatID] = (info, handles)
    }
    guard let (chat, handles) = chats[chatID] else { throw SafeCollectionError.unsafeMetadata }
    if let accountID, chat.accountID != accountID { return skipped }
    let body = schema.hasAttributedBody ? "m.attributedBody" : "NULL"
    let destination = schema.hasDestinationCallerID ? "m.destination_caller_id" : "NULL"
    // Check sizes without materializing an unbounded text/blob. The parser only
    // sees this row's attributed text: never a reply parent, attachment or poll.
    let sizes = try db.prepareRowIterator(
      "SELECT length(CAST(m.text AS BLOB)) AS text_size, length(\(body)) AS body_size FROM message m WHERE m.ROWID = ?",
      bindings: [id])
    guard let size = try sizes.failableNext() else { throw SafeCollectionError.unsafeMetadata }
    guard try (int64Value(size, "text_size") ?? 0) <= 65536,
      try (int64Value(size, "body_size") ?? 0) <= 1048576
    else { throw SafeCollectionError.oversizedText }
    let messages = try db.prepareRowIterator("""
      SELECT m.guid AS guid, h.id AS sender, \(destination) AS destination,
        m.is_from_me AS from_me, m.text AS text, \(body) AS body, m.date AS date
      FROM message m LEFT JOIN handle h ON h.ROWID = m.handle_id WHERE m.ROWID = ?
      """, bindings: [id])
    guard let row = try messages.failableNext(), let direction = try intValue(row, "from_me"),
      direction == 0 || direction == 1, let date = try int64Value(row, "date")
    else { throw SafeCollectionError.unsafeMetadata }
    let guid = try stringValue(row, "guid")
    var sender = try stringValue(row, "sender")
    if sender.isEmpty { sender = try stringValue(row, "destination") }
    var text = try stringValue(row, "text")
    if text.isEmpty { text = try TypedStreamParser.parseAttributedBody(dataValue(row, "body")) }
    guard !guid.isEmpty, guid.utf8.count <= 512, sender.utf8.count <= 320 else {
      throw SafeCollectionError.unsafeMetadata
    }
    guard text.utf8.count <= 65536 else { throw SafeCollectionError.oversizedText }
    return SafeCollectionRow(id: id,
      message: SafeCollectionMessage(guid: guid, sender: sender, fromMe: direction == 1,
        text: text, date: appleDate(from: date)), chat: chat, participants: handles)
  }
}
