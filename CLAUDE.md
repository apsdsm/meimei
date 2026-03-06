# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

meimei is a deployment and build management CLI for AWS CodeDeploy, written in Go. It provides a TUI for browsing builds, selecting deployment targets, and monitoring deployment progress.

Module path: `github.com/apsdsm/meimei`

## Development Commands

```bash
go build -o meimei .    # Build binary
go run main.go          # Run directly
go install .            # Install to GOPATH/bin
go test ./...           # Run tests
```

## Versioning

The version is defined as a `const` in `cmd/version.go`. When bumping the version:
1. Update the `Version` constant in `cmd/version.go`
2. Create a git tag matching the version (e.g. `git tag v0.1.0`)
3. Push the tag (e.g. `git push origin v0.1.0`)
