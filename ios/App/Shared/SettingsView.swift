import SwiftUI
import BowtieKit

/// Server + account settings: change server, change password, sign out.
struct SettingsView: View {
    @Bindable var appModel: AppModel

    @State private var currentPassword = ""
    @State private var newPassword = ""
    @State private var isChangingPassword = false
    @State private var passwordMessage: String?
    @State private var passwordIsError = false
    @State private var isSigningOut = false

    private enum Field: Hashable {
        case current
        case new
    }

    @FocusState private var focusedField: Field?

    /// Spec-mandated success copy (verbatim).
    static let passwordChangedCopy = "Password changed."

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                serverSection
                accountSection
            }
            .padding(24)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity)
        }
        .bowtieScreenBackground()
        .navigationTitle("Settings")
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
    }

    // MARK: - Server

    private var serverSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            sectionHeader("Server")

            VStack(alignment: .leading, spacing: 6) {
                Text("Address")
                    .font(Theme.label(13))
                    .foregroundStyle(Theme.dim)
                Text(appModel.serverURL?.absoluteString ?? "—")
                    .font(Theme.mono(14))
                    .foregroundStyle(Theme.text)
                    #if os(iOS)
                    .textSelection(.enabled)
                    #endif
                    .accessibilityLabel(
                        "Server address, \(appModel.serverURL?.absoluteString ?? "none")"
                    )
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(Theme.fieldPadding)
            .background(Theme.surface)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(Theme.line, lineWidth: 1)
            )

            Button {
                appModel.changeServer()
            } label: {
                Text("Change server")
                    .font(Theme.label(16))
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 12)
                    .background(Theme.raised)
                    .foregroundStyle(Theme.text)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            }
            .buttonStyle(BowtiePlainButtonStyle())
            .accessibilityLabel("Change server")
            .accessibilityHint("Sign out and choose a different Bowtie server")
        }
        .bowtieFocusSection()
    }

    // MARK: - Account

    private var accountSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            sectionHeader("Account")

            VStack(alignment: .leading, spacing: 6) {
                Text("Username")
                    .font(Theme.label(13))
                    .foregroundStyle(Theme.dim)
                Text(appModel.user?.username ?? "—")
                    .font(Theme.body())
                    .foregroundStyle(Theme.text)
                    .accessibilityLabel("Username, \(appModel.user?.username ?? "none")")
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(Theme.fieldPadding)
            .background(Theme.surface)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(Theme.line, lineWidth: 1)
            )

            // SecureField placeholders surface as full-screen keyboard prompts on tvOS.
            VStack(alignment: .leading, spacing: 12) {
                Text("Change password")
                    .font(Theme.label())
                    .foregroundStyle(Theme.dim)

                SecureField("Current password", text: $currentPassword)
                    #if os(iOS)
                    .textContentType(.password)
                    .submitLabel(.next)
                    #endif
                    .focused($focusedField, equals: .current)
                    .themedField(focused: focusedField == .current)
                    .onSubmit { focusedField = .new }
                    .accessibilityLabel("Current password")
                    .disabled(isChangingPassword)

                SecureField("New password", text: $newPassword)
                    #if os(iOS)
                    .textContentType(.newPassword)
                    .submitLabel(.go)
                    #endif
                    .focused($focusedField, equals: .new)
                    .themedField(focused: focusedField == .new)
                    .onSubmit { Task { await changePassword() } }
                    .accessibilityLabel("New password")
                    .disabled(isChangingPassword)

                if let passwordMessage {
                    Text(passwordMessage)
                        .font(Theme.body(15))
                        .foregroundStyle(passwordIsError ? Theme.alert : Theme.signal)
                        .accessibilityLabel(passwordMessage)
                }

                Button {
                    Task { await changePassword() }
                } label: {
                    HStack(spacing: 10) {
                        if isChangingPassword {
                            ProgressView()
                                .tint(Theme.text)
                        }
                        Text("Change password")
                            .font(Theme.label(16))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 12)
                    .background(canChangePassword ? Theme.raised : Theme.surface)
                    .foregroundStyle(canChangePassword ? Theme.text : Theme.dim)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }
                .buttonStyle(BowtiePlainButtonStyle())
                .disabled(!canChangePassword || isChangingPassword)
                .accessibilityLabel("Change password")
                .accessibilityHint("Update your account password")
            }
            .bowtieFocusSection()

            Button {
                Task { await signOut() }
            } label: {
                HStack(spacing: 10) {
                    if isSigningOut {
                        ProgressView()
                            .tint(Theme.alert)
                    }
                    Text("Sign out")
                        .font(Theme.label(16))
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
                .background(Theme.surface)
                .foregroundStyle(Theme.alert)
                .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                        .stroke(Theme.line, lineWidth: 1)
                )
            }
            .buttonStyle(BowtiePlainButtonStyle())
            .disabled(isSigningOut || isChangingPassword)
            .accessibilityLabel("Sign out")
            .accessibilityHint("Sign out and return to the login screen")
            .padding(.top, 8)
        }
        .bowtieFocusSection()
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(Theme.title(18))
            .foregroundStyle(Theme.text)
            .accessibilityAddTraits(.isHeader)
    }

    private var canChangePassword: Bool {
        !currentPassword.isEmpty && !newPassword.isEmpty
    }

    @MainActor
    private func changePassword() async {
        guard canChangePassword, !isChangingPassword else { return }
        guard let client = appModel.client else {
            passwordIsError = true
            passwordMessage = "Not connected."
            return
        }

        isChangingPassword = true
        passwordMessage = nil
        defer { isChangingPassword = false }

        do {
            try await client.changePassword(current: currentPassword, new: newPassword)
            currentPassword = ""
            newPassword = ""
            focusedField = nil
            passwordIsError = false
            passwordMessage = Self.passwordChangedCopy
        } catch let error as BowtieError {
            passwordIsError = true
            switch error {
            case .unauthorized:
                passwordMessage = "Wrong username or password."
            case .server(_, let message), .network(let message), .negotiationFailed(let message):
                passwordMessage = message
            default:
                passwordMessage = "Couldn't change password."
            }
        } catch {
            passwordIsError = true
            passwordMessage = "Couldn't change password."
        }
    }

    @MainActor
    private func signOut() async {
        guard !isSigningOut else { return }
        isSigningOut = true
        defer { isSigningOut = false }
        await appModel.signOut()
    }
}

#Preview {
    NavigationStack {
        SettingsView(appModel: AppModel(store: {
            let store = InMemorySessionStore()
            store.save(server: URL(string: "http://192.168.1.50:8400")!, refreshToken: "tok")
            return store
        }()))
    }
}
