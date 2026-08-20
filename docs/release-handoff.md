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

The package contains its local `codeflow-local` marketplace and the `codeflow`
plugin. `bin/codeflow install` copies the paired Core and adapter into
`HOME/.codeflow`, adds that marketplace through the Codex CLI, and activates the
plugin. A new Codex task then loads the MCP tools; its first flow request starts
or reuses the local Core.
