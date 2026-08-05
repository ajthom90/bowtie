import SwiftUI
import BowtieKit

@main
struct BowtieTVApp: App {
    var body: some Scene {
        WindowGroup {
            HelloView()
        }
    }
}

/// Scaffold placeholder — replaced by channel rail in later tasks.
private struct HelloView: View {
    var body: some View {
        VStack(spacing: 16) {
            Text("Bowtie")
                .font(Theme.channelNumber(64))
                .foregroundStyle(Theme.amber)
            Text("tvOS scaffold")
                .font(Theme.mono(20))
                .foregroundStyle(Theme.dim)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.bg)
    }
}
