# Installation

There are three primary methods to install the `clogs` binary, depending on your needs.

### Using Grove (Recommended)

For users who are part of the Grove developer tool ecosystem, the `grove` meta-tool is the recommended installation method. Grove automatically handles the discovery, installation, and management of binaries for all integrated tools, including `clogs`.

If you are using Grove, `clogs` will be made available to you as part of the standard workflow without needing manual installation steps.

### Downloading from GitHub Releases

Pre-compiled binaries are available for download from the project's [GitHub Releases page](https://github.com/mattsolo1/grove-claude-logs/releases). This is the simplest method if you do not want to build from source.

The release workflow automatically builds binaries for the following platforms:
*   macOS (amd64, arm64)
*   Linux (amd64, arm64)

Download the appropriate archive for your operating system and architecture, extract it, and place the `clogs` binary in a directory within your system's `PATH`.

### Building from Source

If you need to build from the latest source code or are developing `clogs`, you can compile it directly.

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/mattsolo1/grove-claude-logs.git
    ```

2.  **Navigate into the project directory:**
    ```bash
    cd grove-claude-logs
    ```

3.  **Build the binary using the Makefile:**
    ```bash
    make build
    ```

This command compiles the source code and places the final binary at `./bin/clogs`. The Grove ecosystem is designed to discover binaries in this location, so there is no need to move it into your global `PATH`.