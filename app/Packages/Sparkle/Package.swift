// swift-tools-version:5.3
import PackageDescription

let package = Package(
    name: "Sparkle",
    products: [
        .library(name: "Sparkle", targets: ["Sparkle"])
    ],
    targets: [
        .binaryTarget(
            name: "Sparkle",
            path: "Sparkle.xcframework.zip"
        )
    ]
)
