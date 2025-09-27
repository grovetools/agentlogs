# Architecture

This document describes the high-level architecture of the `clogs` project, its directory structure, and how the components work together.

## High-Level Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                     User Interface                       │
├─────────────────────────────────────────────────────────┤
│                    CLI Commands Layer                    │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐  │
│  │   list   │ │   read   │ │  query   │ │   tail   │  │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘  │
├─────────────────────────────────────────────────────────┤
│                   Public API Layer                       │
│              pkg/claudelogs (Go Library)                 │
├─────────────────────────────────────────────────────────┤
│                 Internal Logic Layer                     │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐  │
│  │  Parser  │ │ Monitor  │ │ Database │ │ Summary  │  │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘  │
├─────────────────────────────────────────────────────────┤
│                    Data Sources                          │
│         ~/.claude/projects/*.jsonl files                 │
└─────────────────────────────────────────────────────────┘
```

## Directory Structure

### `cmd/` - CLI Entry Point and Commands

Contains the entry point and command definitions for the `clogs` CLI, built using the `cobra` library for command-line interface management.

**Key Components:**
- **Root Command**: Initializes the CLI application with global flags and configuration
- **Subcommands**: Implements `list`, `read`, `query`, `tail`, and `version` commands
- **Command Handlers**: Each command has its own handler that orchestrates the internal logic

**Technology Stack:**
- `github.com/spf13/cobra` for command structure
- `github.com/spf13/viper` for configuration management
- Direct integration with `internal/` packages for business logic

### `internal/` - Core Application Logic

Holds the core application logic that is not meant to be imported by other projects. This directory contains the heart of the clogs functionality.

**Key Packages:**

#### `internal/transcript/`
- **parser.go**: Parses Claude's JSONL transcript files, handling large messages and extracting structured data
- **monitor.go**: Implements real-time monitoring of transcript files, detecting changes and processing new messages
- **database.go**: Manages SQLite database operations for storing and querying extracted messages
- **summary.go**: Handles AI-powered summarization of conversations using external LLM commands
- **utils.go**: Common utility functions for file operations and data transformations

#### `internal/data/`
- **grove_plans.go**: Integration with Grove Flow plans for session management
- **models.go**: Data structures and models used throughout the application

### `pkg/` - Public Go Library

Exposes the core logic as a reusable public Go library (`claudelogs`) for other applications to consume. This is the public API surface of clogs.

**Package Structure:**
```
pkg/claudelogs/
├── parser.go      # Public parsing interface
├── monitor.go     # Public monitoring interface
├── types.go       # Public types and structures
└── config.go      # Configuration options
```

**Design Pattern**: The `pkg/` directory acts as a facade/wrapper around the `internal/` packages, providing a clean, stable API while hiding implementation details.

### `tests/e2e/` - End-to-End Testing

Contains end-to-end tests for the CLI using the `grove-tend` framework.

**Test Structure:**
- **YAML Test Definitions**: Tests are defined in YAML format compatible with grove-tend
- **Integration Tests**: Verify the complete flow from CLI invocation to output
- **Regression Tests**: Ensure backward compatibility and prevent feature regression

## Component Interactions

### Data Flow for CLI Usage

1. **User invokes CLI command** → `main.go` entry point
2. **Cobra parses arguments** → Routes to appropriate command handler in `cmd/`
3. **Command handler calls internal logic** → `internal/transcript/` packages
4. **Internal logic processes data** → Reads transcript files, queries database, or monitors changes
5. **Results formatted and returned** → Output to terminal (table, JSON, or text format)

### Data Flow for Library Usage

1. **External application imports** → `pkg/claudelogs`
2. **Creates parser or monitor instance** → Using public constructors
3. **Configures with options** → Sets up database, intervals, callbacks
4. **Invokes methods** → Parse files, start monitoring, query data
5. **Receives structured data** → Through return values or callbacks

## Key Design Patterns

### 1. **Wrapper/Facade Pattern**
The `pkg/claudelogs` package wraps the complex internal implementation, providing a simplified interface for external consumers.

### 2. **Command Pattern**
Each CLI command is encapsulated as a separate handler with its own execution logic, making the system extensible and maintainable.

### 3. **Repository Pattern**
Database operations are abstracted through a repository-like interface in `database.go`, separating data access from business logic.

### 4. **Observer/Monitor Pattern**
The monitor component implements a file-watching pattern, observing transcript files and notifying about changes.

## Integration Points

### Grove Ecosystem Integration
- **Binary Management**: Binaries are built to `./bin/` and managed by the Grove meta-tool
- **Session Discovery**: Integrates with Grove's session management for finding relevant transcript files
- **Testing Framework**: Uses grove-tend for comprehensive end-to-end testing

### Claude Integration
- **File Format**: Reads Claude's native JSONL transcript format
- **Project Structure**: Understands Claude's project organization in `~/.claude/projects/`
- **Real-time Support**: Monitors active Claude sessions as they generate new messages

### External Systems
- **SQLite Database**: For persistent storage and efficient querying of parsed messages
- **LLM Commands**: Configurable integration with external LLM tools for summarization
- **JSON Output**: Structured output format for integration with other tools

## Build and Deployment Architecture

### Build System
- **Makefile**: Primary build orchestration
- **Go Modules**: Dependency management
- **Version Injection**: Build-time version information injection via LDFLAGS
- **Cross-platform Support**: Builds for multiple OS/architecture combinations

### Testing Strategy
- **Unit Tests**: Internal package testing (not shown in structure but implied)
- **E2E Tests**: Comprehensive CLI testing with grove-tend
- **Integration Tests**: Database and file system interaction testing

## Summary

The `clogs` architecture exemplifies clean separation of concerns with:
- Clear distinction between CLI interface and reusable library code
- Well-defined internal logic that's protected from external dependencies
- Robust testing infrastructure
- Seamless integration with the Grove ecosystem
- Flexibility for both standalone CLI usage and library integration

This architecture allows `clogs` to serve dual purposes effectively: as a powerful standalone CLI tool for developers working with Claude transcripts, and as a reusable Go library for building custom applications that need to process Claude session data.