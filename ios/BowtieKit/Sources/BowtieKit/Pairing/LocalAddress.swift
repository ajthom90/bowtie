import Foundation
#if canImport(Darwin)
import Darwin
#endif

public enum LocalAddress {
    /// First up, non-loopback IPv4 on an `en*`/`eth*` interface, else the first non-loopback IPv4.
    public static func lanIPv4() -> String? {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return nil }
        defer { freeifaddrs(head) }

        var preferred: String?
        var fallback: String?
        var cursor: UnsafeMutablePointer<ifaddrs>? = first
        while let entry = cursor {
            defer { cursor = entry.pointee.ifa_next }
            let flags = Int32(entry.pointee.ifa_flags)
            guard flags & IFF_UP != 0, flags & IFF_LOOPBACK == 0,
                  let addr = entry.pointee.ifa_addr, addr.pointee.sa_family == UInt8(AF_INET) else { continue }

            var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            guard getnameinfo(addr, socklen_t(addr.pointee.sa_len), &host, socklen_t(host.count),
                              nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let ip = String(cString: host)
            let name = String(cString: entry.pointee.ifa_name)
            if name.hasPrefix("en") || name.hasPrefix("eth") {
                if preferred == nil { preferred = ip }
            } else if fallback == nil {
                fallback = ip
            }
        }
        return preferred ?? fallback
    }
}
