// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "BowtieKit",
    platforms: [
        .iOS(.v17),
        .tvOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(name: "BowtieKit", targets: ["BowtieKit"]),
    ],
    targets: [
        .target(name: "BowtieKit"),
        .testTarget(
            name: "BowtieKitTests",
            dependencies: ["BowtieKit"]
        ),
    ]
)
