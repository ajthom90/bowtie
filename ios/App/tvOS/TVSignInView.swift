import SwiftUI
import UIKit
import BowtieKit

/// tvOS sign-in: "Sign in with your phone" (QR code + typed code) first, with
/// the username / password form one button away.
struct TVSignInView: View {
    @Bindable var appModel: AppModel
    @State private var usePassword = false

    var body: some View {
        if usePassword || appModel.client == nil {
            LoginView(appModel: appModel, onUsePhone: appModel.client == nil ? nil : { usePassword = false })
        } else if let client = appModel.client {
            TVPhoneSignInView(appModel: appModel, client: client) {
                usePassword = true
            }
            // A new server gets a new code.
            .id(ObjectIdentifier(client))
        }
    }
}

// MARK: - Phone sign-in

private struct TVPhoneSignInView: View {
    @Bindable var appModel: AppModel
    let client: BowtieClient
    let onUsePassword: () -> Void

    @State private var model: DeviceSignInModel?

    private static let qrSize: CGFloat = 440

    var body: some View {
        HStack(alignment: .center, spacing: 80) {
            qrPanel
            VStack(alignment: .leading, spacing: 28) {
                Text("Sign in with your phone")
                    .font(Theme.title(48))
                    .foregroundStyle(Theme.text)
                    .accessibilityAddTraits(.isHeader)
                statusPanel
                buttons
            }
            .frame(maxWidth: 900, alignment: .leading)
        }
        .padding(80)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .bowtieScreenBackground()
        .task {
            let current = model ?? DeviceSignInModel(client: client, deviceName: UIDevice.current.name)
            model = current
            await current.start()
        }
        .onDisappear {
            model?.cancel()
        }
        .onChange(of: model?.state) { _, state in
            if case .signedIn(let user)? = state {
                appModel.completeDeviceSignIn(user: user)
            }
        }
    }

    // MARK: QR

    @ViewBuilder
    private var qrPanel: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 24, style: .continuous)
                .fill(Color.white)
            if case .waiting(let code)? = model?.state {
                qrImage(code)
            } else {
                ProgressView()
                    .tint(Theme.bg)
                    .opacity(isWorking ? 1 : 0)
            }
        }
        .frame(width: Self.qrSize, height: Self.qrSize)
        .opacity(isWaiting ? 1 : 0.35)
    }

    private func qrImage(_ code: DeviceSignInModel.Code) -> some View {
        AsyncImage(url: code.qrURL) { phase in
            if let image = phase.image {
                image
                    .interpolation(.none)
                    .resizable()
                    .scaledToFit()
                    .padding(24)
            } else if phase.error != nil {
                Image(systemName: "qrcode")
                    .font(.system(size: 120))
                    .foregroundStyle(Theme.bg.opacity(0.4))
            } else {
                ProgressView()
                    .tint(Theme.bg)
            }
        }
        .accessibilityLabel("QR code to sign in with your phone")
    }

    // MARK: Status

    @ViewBuilder
    private var statusPanel: some View {
        switch model?.state {
        case .waiting(let code)?:
            waitingText(code)
        case .expired?:
            message("That code expired. Get a new one to keep going.", color: Theme.dim)
        case .failed(let text)?:
            message(text, color: Theme.alert)
        case .signedIn?:
            message("Signed in.", color: Theme.signal)
        case .idle?, .starting?, nil:
            message("Getting a code…", color: Theme.dim)
        }
    }

    private func waitingText(_ code: DeviceSignInModel.Code) -> some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Point your phone's camera at the code, then approve this Apple TV on the page that opens.")
                .font(Theme.body(28))
                .foregroundStyle(Theme.dim)
                .fixedSize(horizontal: false, vertical: true)
            Text("Or go to \(code.linkText) and enter")
                .font(Theme.body(28))
                .foregroundStyle(Theme.dim)
                .fixedSize(horizontal: false, vertical: true)
            Text(code.userCode)
                .font(Theme.mono(72))
                .foregroundStyle(Theme.amber)
                .accessibilityLabel("Code \(code.userCode.map(String.init).joined(separator: " "))")
        }
    }

    private func message(_ text: String, color: Color) -> some View {
        Text(text)
            .font(Theme.body(28))
            .foregroundStyle(color)
            .fixedSize(horizontal: false, vertical: true)
    }

    // MARK: Buttons

    private var buttons: some View {
        VStack(alignment: .leading, spacing: 16) {
            if needsNewCode {
                Button("Get a New Code") {
                    guard let model else { return }
                    Task { await model.start() }
                }
            }
            Button("Use a Password Instead") {
                model?.cancel()
                onUsePassword()
            }
            Button("Change Server") {
                model?.cancel()
                appModel.changeServer()
            }
        }
        .font(Theme.label(24))
        .focusSection()
    }

    // MARK: State helpers

    private var isWaiting: Bool {
        if case .waiting? = model?.state { return true }
        return false
    }

    private var isWorking: Bool {
        switch model?.state {
        case .starting?, .idle?, nil: return true
        default: return false
        }
    }

    private var needsNewCode: Bool {
        switch model?.state {
        case .expired?, .failed?: return true
        default: return false
        }
    }
}
