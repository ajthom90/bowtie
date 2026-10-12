package app.bowtie.core.pairing

import java.net.Inet4Address
import java.net.NetworkInterface

object LanAddress {
    /** This device's private IPv4 address on the local network, preferring Wi-Fi/Ethernet; null when offline. */
    fun ipv4(): String? {
        val candidates = runCatching { NetworkInterface.getNetworkInterfaces()?.toList() }.getOrNull().orEmpty()
            .filter { runCatching { it.isUp && !it.isLoopback }.getOrDefault(false) }
            .flatMap { nif ->
                nif.inetAddresses.toList()
                    .filterIsInstance<Inet4Address>()
                    .filter { it.isSiteLocalAddress }
                    .map { nif.name to it.hostAddress }
            }
        return (candidates.firstOrNull { (name, _) -> name.startsWith("wlan") || name.startsWith("eth") }
            ?: candidates.firstOrNull())?.second
    }
}
