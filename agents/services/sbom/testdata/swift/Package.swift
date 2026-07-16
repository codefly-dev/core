// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "CodeflySBOMFixture",
    products: [
        .library(name: "CodeflySBOMFixture", targets: ["CodeflySBOMFixture"]),
    ],
    targets: [
        .target(name: "CodeflySBOMFixture"),
    ]
)
