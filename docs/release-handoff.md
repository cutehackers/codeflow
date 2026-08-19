# Release handoff

The implementation and its local/isolated package tests are complete. Two
external release actions remain deliberately unperformed.

## CF-G14: macOS distribution

`make package-macos` creates a relocatable arm64 layout with the Core, owned
Dart adapter, and compatibility manifest. The produced executable is only
ad-hoc signed (`TeamIdentifier=not set`), not a signed release artifact.

Complete one of these paths:

1. provide a Developer ID signing/notarization identity and release channel;
2. provide the Git remote/release URL and Homebrew tap for a distributable
   formula and clean-machine installation test.

## CF-G15: Codex plugin installation

`plugins/codeflow` validates and its packaged MCP/hook integration tests pass.
It is not registered in a marketplace. Select either the default personal
marketplace under `HOME/.agents/plugins/marketplace.json` or a repository/team
marketplace path before installation; the latter needs the intended remote
source and marketplace destination.
