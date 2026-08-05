import SwiftUI
import BowtieKit

@main
struct BowtieApp: App {
    var body: some Scene {
        WindowGroup {
            HelloView()
        }
    }
}

/// Scaffold placeholder — replaced by Connect/Login flow in later tasks.
private struct HelloView: View {
    var body: some View {
        VStack(spacing: 12) {
            Text("Bowtie")
                .font(Theme.channelNumber(48))
                .foregroundStyle(Theme.amber)
            Text("iOS scaffold")
                .font(Theme.mono(14))
                .foregroundStyle(Theme.dim)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.bg)
    }
}
