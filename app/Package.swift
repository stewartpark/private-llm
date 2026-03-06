// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "PrivateLLM",
    platforms: [.macOS(.v13)],
    dependencies: [
        .package(path: "Packages/Sparkle"),
        .package(url: "https://github.com/migueldeicaza/SwiftTerm", exact: "1.2.3"),
    ],
    targets: [
        .executableTarget(
            name: "PrivateLLM",
            dependencies: ["SwiftTerm", "Sparkle"],
            path: "Sources/PrivateLLM"
        ),
    ]
)
