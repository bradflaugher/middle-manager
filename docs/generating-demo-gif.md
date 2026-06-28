# Generating the README Demo GIF

This repository uses an animated GIF to demonstrate the `middle-manager` terminal user interface in action. This document details how this GIF is programmatically generated without requiring active API keys.

## Overview

The recording is generated using a combination of a virtual pseudo-terminal (`pty`), a Python terminal emulator (`pyte`), and the Python Imaging Library (`Pillow` / `PIL`).

The architecture works as follows:

```
┌──────────────────┐               ┌──────────────┐
│  middle-manager  │               │ generate_gif │
│      binary      │ ──[pty I/O]──►│    script    │
└──────────────────┘               └──────────────┘
                                          │
                                       (parser)
                                          │
                                          ▼
                                   ┌──────────────┐
                                   │  pyte cells  │
                                   └──────────────┘
                                          │
                                      (renderer)
                                          │
                                          ▼
                                   ┌──────────────┐
                                   │ Pillow image │
                                   └──────────────┘
```

1. **Virtual Terminal Spawn:** We launch a `middle-manager` subprocess within a pseudo-terminal (`pty.openpty()`), forcing the binary to run in full interactive Bubble Tea TUI dashboard mode.
2. **Mock Agent Commands:** We instruct middle-manager to use our [mock_agent.py](scripts/mock_agent.py) script instead of real agent binaries (e.g. Claude Code, OpenCode, Grok) by passing `--binary agent_name=path/to/mock_agent.py`. The mock agent:
   - Identifies the current step (Discover, Execute, Verify, or Commit) from the CLI options/prompts.
   - Streams realistic-looking console outputs line by line with natural delays.
   - Modifies `test-repo/main.py` in the execute step to add a Python docstring.
   - Emits a `VERDICT: PASS` during verification.
3. **Capture and Parsing:** The terminal output stream is parsed in real-time by a `pyte.Screen` emulator. This tracks the positions of all characters, bold text attributes, cursors, and custom ANSI true colors (hex codes like synthwave violet/magenta).
4. **Variable Frame-Rate Rendering:** At 100ms intervals, we inspect the screen. If the screen has changed:
   - We render the screen's character grid onto a Pillow `Image` using monospace font tiles (Liberation Mono).
   - If the screen has not changed, we simply increment the timing duration of the previous frame. This compression yields a small file size (**~850 KB**) while preserving pauses.

## Scripts

The source code for generating this demo is checked into the repository:
* **[mock_agent.py](scripts/mock_agent.py)**: Simulates the responses of the four pipeline agents.
* **[generate_gif.py](scripts/generate_gif.py)**: Builds the local `mm` binary, prepares an ignored `test-repo`, spawns the virtual terminal, captures and renders frames, compresses duplicates, and outputs `docs/interface_demo.gif`.

## Setup and Usage

To run the generation script locally, install the Python dependencies first:

### 1. Install Python Dependencies
```bash
pip install pyte wcwidth Pillow
```

### 2. Run the Generator
```bash
python3 docs/scripts/generate_gif.py
```
This builds the local `mm` binary, runs the full 4-step loop simulation against an ignored local `test-repo` directory, and updates `docs/interface_demo.gif`.
