import Darwin
import Foundation

@main struct ContactsTransportTests {
    static func main() throws {
        let pipe = Pipe()
        let payload = Data("{\"v\":1}".utf8)
        try writeContactFrame(pipe.fileHandleForWriting, data: payload)
        let got = try readContactFrame(pipe.fileHandleForReading, maximum: 100)
        precondition(got == payload)
        try pipe.fileHandleForWriting.write(contentsOf: Data([0, 1, 0, 1]))
        do {
            _ = try readContactFrame(pipe.fileHandleForReading, maximum: 65536)
            fatalError("oversized input accepted")
        } catch ContactsFailure.overflow {}
        try pipe.fileHandleForWriting.close()
        do {
            _ = try readContactFrame(pipe.fileHandleForReading, maximum: 100)
            fatalError("truncated frame accepted")
        } catch ContactsFailure.invalid {}
        do {
            try connectOwnerChannel("relative/socket")
            fatalError("relative socket accepted")
        } catch ContactsFailure.invalid {}
        print("Contacts transport tests passed")
    }
}
