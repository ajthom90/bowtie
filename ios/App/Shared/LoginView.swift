import SwiftUI
import BowtieKit

/// Username / password sign-in against the connected server.
struct LoginView: View {
    @Bindable var appModel: AppModel

    @State private var username = ""
    @State private var password = ""
    @State private var isSigningIn = false
    @State private var errorMessage: String?

    private enum Field: Hashable {
        case username
        case password
    }

    @FocusState private var focusedField: Field?

    /// Spec-mandated auth failure copy (verbatim).
    static let wrongCredentialsCopy = "Wrong username or password."

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                header

                VStack(alignment: .leading, spacing: 16) {
                    fieldBlock(title: "Username", field: .username) {
                        TextField("Username", text: $username)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            #if os(iOS)
                            .textContentType(.username)
                            .keyboardType(.asciiCapable)
                            .submitLabel(.next)
                            #endif
                            .focused($focusedField, equals: .username)
                            .themedField(focused: focusedField == .username)
                            .onSubmit { focusedField = .password }
                            .accessibilityLabel("Username")
                            .disabled(isSigningIn)
                    }

                    fieldBlock(title: "Password", field: .password) {
                        SecureField("Password", text: $password)
                            #if os(iOS)
                            .textContentType(.password)
                            .submitLabel(.go)
                            #endif
                            .focused($focusedField, equals: .password)
                            .themedField(focused: focusedField == .password)
                            .onSubmit { Task { await signIn() } }
                            .accessibilityLabel("Password")
                            .disabled(isSigningIn)
                    }
                }

                if let errorMessage {
                    Text(errorMessage)
                        .font(Theme.body(15))
                        .foregroundStyle(Theme.alert)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityLabel(errorMessage)
                }

                Button {
                    Task { await signIn() }
                } label: {
                    HStack(spacing: 10) {
                        if isSigningIn {
                            ProgressView()
                                .tint(Theme.bg)
                        }
                        Text(isSigningIn ? "Signing in…" : "Sign in")
                            .font(Theme.label(17))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 14)
                    .background(canSignIn ? Theme.amber : Theme.raised)
                    .foregroundStyle(canSignIn ? Theme.bg : Theme.dim)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }
                .buttonStyle(.plain)
                .disabled(!canSignIn || isSigningIn)
                .accessibilityLabel("Sign in")
                .accessibilityHint("Sign in with your Bowtie account")
                .accessibilityAddTraits(.isButton)

                if appModel.serverURL != nil {
                    Button("Change server") {
                        appModel.changeServer()
                    }
                    .font(Theme.body(15))
                    .foregroundStyle(Theme.dim)
                    .buttonStyle(.plain)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 4)
                    .accessibilityLabel("Change server")
                    .accessibilityHint("Return to the connect screen to pick a different server")
                    .disabled(isSigningIn)
                }
            }
            .padding(24)
            .frame(maxWidth: 480)
            .frame(maxWidth: .infinity)
        }
        .bowtieScreenBackground()
        .onAppear { focusedField = .username }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Sign in")
                .font(Theme.title(28))
                .foregroundStyle(Theme.text)
                .accessibilityAddTraits(.isHeader)

            if let host = appModel.serverURL?.absoluteString {
                Text(host)
                    .font(Theme.mono(13))
                    .foregroundStyle(Theme.dim)
                    .lineLimit(2)
                    .accessibilityLabel("Server \(host)")
            }
        }
    }

    private func fieldBlock<Content: View>(
        title: String,
        field: Field,
        @ViewBuilder content: () -> Content
    ) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(Theme.label())
                .foregroundStyle(Theme.dim)
                .accessibilityHidden(true)
            content()
        }
    }

    private var canSignIn: Bool {
        !username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !password.isEmpty
    }

    @MainActor
    private func signIn() async {
        let user = username.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !user.isEmpty, !password.isEmpty, !isSigningIn else { return }

        isSigningIn = true
        errorMessage = nil
        defer { isSigningIn = false }

        do {
            try await appModel.signIn(username: user, password: password)
        } catch let error as BowtieError {
            switch error {
            case .unauthorized:
                errorMessage = Self.wrongCredentialsCopy
            case .network(let message):
                errorMessage = message
            case .server(_, let message):
                errorMessage = message
            default:
                errorMessage = Self.wrongCredentialsCopy
            }
        } catch {
            errorMessage = Self.wrongCredentialsCopy
        }
    }
}

#Preview {
    LoginView(appModel: AppModel(store: {
        let store = InMemorySessionStore()
        store.save(server: URL(string: "http://192.168.1.50:8400")!, refreshToken: nil)
        return store
    }()))
}
