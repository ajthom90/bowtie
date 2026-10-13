import SwiftUI
import UIKit
import CoreImage.CIFilterBuiltins

/// The "set up this TV from your phone" QR and its caption; nothing while pairing is unavailable.
struct PairingQRView: View {
    let url: URL?

    private static let qrSize: CGFloat = 440

    var body: some View {
        if let url {
            VStack(spacing: 24) {
                ZStack {
                    RoundedRectangle(cornerRadius: 24, style: .continuous)
                        .fill(Color.white)
                    if let image = Self.qrImage(for: url.absoluteString) {
                        Image(uiImage: image)
                            .interpolation(.none)
                            .resizable()
                            .scaledToFit()
                            .padding(24)
                            .accessibilityLabel("QR code to set up this TV from your phone")
                    }
                }
                .frame(width: Self.qrSize, height: Self.qrSize)

                VStack(spacing: 8) {
                    Text("Scan with your phone to set up this TV")
                        .font(Theme.title(32))
                        .foregroundStyle(Theme.text)
                    Text("Or open \(url.absoluteString) on your phone")
                        .font(Theme.body(22))
                        .foregroundStyle(Theme.dim)
                }
                .multilineTextAlignment(.center)
                .frame(maxWidth: Self.qrSize + 120)
            }
        } else {
            EmptyView()
        }
    }

    private static func qrImage(for text: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 12, y: 12)),
              let cg = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: cg)
    }
}
