## v0.6.0 (2026-02-02)

Session management has been improved with XDG-compliant path resolution (26193ce), moving away from hardcoded paths for better system integration. Additionally, session resolution logic has been fixed to correctly prioritize the most recent session when multiple sessions exist for the same job (5f61c43).

Visual improvements include properly enabling tool formatters for Edit/Write operations (083762a), ensuring diffs are displayed correctly in the unified view. Internal configuration has been migrated from YAML to TOML (efe4741), and the MIT License has been formally added (77c9f40).

### Bug Fixes
- Match the most recent session when multiple sessions have the same job (5f61c43)
- Enable tool formatters in unified display for Edit/Write diffs (083762a)
- Update VERSION_PKG to grovetools/core path (507a782)

### Refactoring
- Use XDG-compliant paths for session scanning (26193ce)
- Update docgen title to match package name (1a93086)

### Documentation
- Update readme/overview (bdb0c6f)
- Add concept lookup instructions to CLAUDE.md (4a5845e)
- Add MIT License (77c9f40)

### Chores
- Update go.mod for grovetools migration (55b910e)
- Migrate grove.yml to grove.toml (efe4741)
- Move README template to notebook (e8a98ee)
- Remove docgen files from repo (4d9cc82)
- Move docs.rules to .cx/ directory (2c1aeac)
- Update docs.json (ba0f82e)
- Update docs/makefile (208cd1b)
- Restore release workflow (78726c7)

### File Changes
```
 .cx/docs.rules                | 11 +++++++++
 .github/workflows/release.yml | 51 ++++++++++++++++++++++++++++++++++++++++++
 CLAUDE.md                     | 15 ++++++++++++-
 LICENSE                       | 21 +++++++++++++++++
 Makefile                      | 16 ++++++++-----
 README.md                     | 52 ++++++++++++++++++++++---------------------
 cmd/getSessionInfo.go         |  7 ++++++
 config/aglogs.schema.json     |  2 +-
 docs/01-overview.md           | 51 +++++++++++++++++++++---------------------
 docs/05-configuration.md      | 18 +++++++++++++++
 docs/README.md.tpl            |  6 -----
 docs/docgen.config.yml        | 32 --------------------------
 docs/docs.rules               |  1 -
 go.mod                        | 11 +++++++--
 go.sum                        | 49 ++++++++++++++++++++++++++++++++++++----
 grove.toml                    | 10 +++++++++
 grove.yml                     |  9 --------
 internal/display/unified.go   | 13 +++++++++++
 internal/session/resolver.go  |  7 ++++++
 internal/session/scanner.go   | 10 +++------
 pkg/docs/docs.json            | 22 +++++++++---------
 21 files changed, 284 insertions(+), 130 deletions(-)
```

## v0.1.1-nightly.9e6a2c9 (2025-10-03)

## v0.1.0 (2025-10-01)

This release introduces a comprehensive documentation suite, generated using `docgen`. The documentation has been structured into an overview, practical examples, and a full command reference (5a4b6ef, e89acbe). The generation process itself was improved with support for a Table of Contents (e2eafab), more succinct content (e1d10cf), and standardized configuration (a6ebb43).

The release process is now more robust, with the workflow updated to extract release notes directly from `CHANGELOG.md` to ensure consistency (5794e14). Additionally, the CI workflow was fixed to prevent unintended executions (dc35951) and streamlined by removing redundant tests (f5c1016).

### Features

- Update release workflow to use CHANGELOG.md (5794e14)
- Add TOC generation and docgen configuration updates (e2eafab)
- Make documentation generation more succinct and configurable (e1d10cf)

### Bug Fixes

- Update CI workflow to use 'branches: [ none ]' to prevent execution (dc35951)
- Clean up README.md.tpl template format (feed5b0)

### Documentation

- Add initial documentation structure and content (e89acbe)
- Simplify installation instructions to point to main Grove guide (ea48cfd)
- Rename 'Introduction' sections to 'Overview' for clarity (fff8874)
- Simplify documentation to a minimal three-part structure (5a4b6ef)
- Update docgen config and enhance overview prompt (6c550ae)
- Update docgen configuration and README templates for TOC support (5348a0c)

### Code Refactoring

- Standardize docgen.config.yml key order and settings (a6ebb43)

### Chores

- Update .gitignore rules for go.work and CLAUDE.md (de83a49)
- Standardize documentation filenames to DD-name.md convention (9843ec0)
- Temporarily disable CI workflow (b9ee58a)

### Continuous Integration

- Remove redundant tests from release workflow (f5c1016)

### File Changes

```
 .github/workflows/ci.yml             |   4 +-
 .github/workflows/release.yml        |  26 ++--
 .gitignore                           |   3 +
 CLAUDE.md                            |  30 +++++
 Makefile                             |   8 +-
 README.md                            |  51 ++++++++
 docs/01-overview.md                  |  38 ++++++
 docs/02-examples.md                  | 233 +++++++++++++++++++++++++++++++++++
 docs/03-command-reference.md         | 186 ++++++++++++++++++++++++++++
 docs/README.md.tpl                   |   6 +
 docs/docgen.config.yml               |  33 +++++
 docs/docs.rules                      |   1 +
 docs/prompts/01-overview.md          |  36 ++++++
 docs/prompts/02-examples.md          |  23 ++++
 docs/prompts/03-command-reference.md |   8 ++
 pkg/docs/docs.json                   | 110 +++++++++++++++++
 16 files changed, 778 insertions(+), 18 deletions(-)
```

## v0.0.8 (2025-09-17)

### Chores

* bump dependencies

## v0.0.7 (2025-09-13)

### Chores

* update Grove dependencies to latest versions

## v0.0.6 (2025-09-04)

### Chores

* **deps:** sync Grove dependencies to latest versions

### Bug Fixes

* prevent truncation in clogs read command

## v0.0.5 (2025-08-27)

### Bug Fixes

* add version command

## v0.0.4 (2025-08-25)

### Continuous Integration

* add Git LFS disable to release workflow

### Features

* enhance clogs with improved list, filtering, and read commands

### Chores

* bump dependencies

## v0.0.3 (2025-08-15)

### Code Refactoring

* standardize E2E binary naming and use grove.yml for binary discovery

### Continuous Integration

* switch to Linux runners to reduce costs
* consolidate to single test job on macOS
* reduce test matrix to macOS with Go 1.24.4 only

### Chores

* **deps:** bump dependencies
* bump deps

## v0.0.2 (2025-08-12)

### Bug Fixes

* build targets

