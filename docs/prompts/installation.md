Document the installation process for the `clogs` binary.

1. The primary installation method is via the `grove` meta-tool. Explain that users within the Grove ecosystem should use that.
2. The secondary method is by downloading pre-compiled binaries from the project's GitHub Releases page. Refer to the `release.yml` workflow which builds for multiple platforms.
3. The third method is building from source. Provide instructions using the `Makefile`: `git clone ...`, then `make build`. Mention the resulting binary is in `./bin/clogs`.