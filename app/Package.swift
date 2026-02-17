// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "PrivateLLM",
    platforms: [.macOS(.v13)],
    dependencies: [
        .package(url: "https://github.com/migueldeicaza/SwiftTerm", exact: "1.2.3"),
    ],
    targets: [
        .executableTarget(
            name: "PrivateLLM",
            dependencies: ["SwiftTerm"],
            path: "Sources/PrivateLLM"
        ),
    ]
)
