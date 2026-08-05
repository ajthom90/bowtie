import SwiftUI
import BowtieKit

/// First-launch screen: enter server URL and validate via `/healthz`.
struct ConnectView: View {
    @Bindable var appModel: AppModel

    @State private var urlText = ""
    @State private var isValidating = false
    @State private var errorMessage: String?

    @FocusState private var urlFocused: Bool

    /// Spec-mandated failure copy (verbatim).
    static let unreachableCopy =
        "Couldn't reach a Bowtie server there. Check the address and try again."

    /// Placeholder shows both URL forms from the design spec.
    static let placeholder = "https://tv.example.com or http://192.168.1.50:8400"

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                header

                VStack(alignment: .leading, spacing: 8) {
                    Text("Server address")
                        .font(Theme.label())
                        .foregroundStyle(Theme.dim)
                        .accessibilityHidden(true)

                    TextField(Self.placeholder, text: $urlText)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        #if os(iOS)
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .submitLabel(.go)
                        #endif
                        .focused($urlFocused)
                        .themedField(focused: urlFocused)
                        .onSubmit { Task { await validate() } }
                        .accessibilityLabel("Server address")
                        .accessibilityHint("Enter your Bowtie server URL")
                        .disabled(isValidating)
                }

                if let errorMessage {
                    Text(errorMessage)
                        .font(Theme.body(15))
                        .foregroundStyle(Theme.alert)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityLabel(errorMessage)
                }

                Button {
                    Task { await validate() }
                } label: {
                    HStack(spacing: 10) {
                        if isValidating {
                            ProgressView()
                                .tint(Theme.bg)
                        }
                        Text(isValidating ? "Validating…" : "Validate")
                            .font(Theme.label(17))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 14)
                    .background(canValidate ? Theme.amber : Theme.raised)
                    .foregroundStyle(canValidate ? Theme.bg : Theme.dim)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }
                .buttonStyle(.plain)
                .disabled(!canValidate || isValidating)
                .accessibilityLabel("Validate")
                .accessibilityHint("Check that a Bowtie server is reachable at this address")
                .accessibilityAddTraits(.isButton)
            }
            .padding(24)
            .frame(maxWidth: 480)
            .frame(maxWidth: .infinity)
        }
        .bowtieScreenBackground()
        .onAppear { urlFocused = true }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Bowtie")
                .font(Theme.channelNumber(40))
                .foregroundStyle(Theme.amber)
                .accessibilityAddTraits(.isHeader)

            Text("Connect to your server")
                .font(Theme.title(20))
                .foregroundStyle(Theme.text)

            Text("Enter the address of the Bowtie server on your network.")
                .font(Theme.body(15))
                .foregroundStyle(Theme.dim)
        }
    }

    private var canValidate: Bool {
        !urlText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    @MainActor
    private func validate() async {
        let raw = urlText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty, !isValidating else { return }

        isValidating = true
        errorMessage = nil
        defer { isValidating = false }

        let ok = await appModel.connect(rawURL: raw)
        if !ok {
            errorMessage = Self.unreachableCopy
        }
    }
}

#Preview {
    ConnectView(appModel: AppModel(store: InMemorySessionStore()))
}
