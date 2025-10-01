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

