import Commander
import Foundation
import IMsgCore

enum CollectCommand {
  static let spec = CommandSpec(
    name: "collect",
    abstract: "Read one bounded physical-row page for safe-imsg",
    discussion: "Read-only insertion cursor; emits raw row envelopes and a final checkpoint.",
    signature: CommandSignatures.withRuntimeFlags(CommandSignature(options:
      CommandSignatures.baseOptions() + [
        .make(label: "after", names: [.long("since-rowid")], help: "exclusive row position (zero includes the first row)"),
        .make(label: "through", names: [.long("through-rowid")], help: "fixed inclusive cycle boundary"),
        .make(label: "limit", names: [.long("limit")], help: "maximum physical rows, 1 to 1000"),
        .make(label: "account", names: [.long("account-id")], help: "exact native account ID from owner configuration"),
      ])),
    usageExamples: ["imsg collect --since-rowid 0 --limit 100 --account-id ACCOUNT --json"]
  ) { values, runtime in
    guard runtime.jsonOutput, let after = values.optionInt64("after"),
      let limit = values.optionInt("limit"),
      let account = values.option("account"), !account.isEmpty,
      values.option("through") == nil || values.optionInt64("through") != nil
    else { throw SafeCollectionError.invalidBounds }
    let store = try MessageStore(path: values.option("db") ?? MessageStore.defaultPath)
    let page = try store.safeCollection(afterRowID: after,
      throughRowID: values.optionInt64("through"), limit: limit, accountID: account)
    for row in page.rows {
      var payload: [String: Any] = ["kind": "row", "row_id": row.id]
      if let message = row.message, let chat = row.chat {
        let group = isGroupHandle(identifier: chat.identifier, guid: chat.guid)
        var metadata: [String: Any] = ["id": chat.id, "identifier": chat.identifier,
          "guid": chat.guid, "service": chat.service, "is_group": group,
          "participants": row.participants]
        if let account = chat.accountID { metadata["account_id"] = account }
        payload["chat"] = metadata
        payload["message"] = ["id": row.id, "chat_id": chat.id, "guid": message.guid,
          "sender": message.sender, "is_from_me": message.fromMe, "text": message.text,
          "created_at": CLIISO8601.format(message.date), "chat_identifier": chat.identifier,
          "chat_guid": chat.guid, "is_group": group, "participants": row.participants]
      }
      try JSONLines.printObject(payload)
    }
    try JSONLines.printObject(["kind": "checkpoint", "schema": "safe-imsg.collect.v1",
      "through_row_id": page.throughRowID, "scanned_through_row_id": page.scannedThroughRowID,
      "complete": page.complete])
  }
}
