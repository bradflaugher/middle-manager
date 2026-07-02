# Generating the README Demo GIF

This repository uses an animated GIF to demonstrate the `middle-manager` terminal user interface in action — the **full first-run experience**: the interactive setup wizard (mission typing, loop shape, agents, factory options, the agent strength-ordering drag) followed by a live monitored 4-step loop to completion. This document details how this GIF is programmatically generated without requiring active API keys or any human at the keyboard.

## Overview

The recording is generated using a combination of a virtual pseudo-terminal (`pty`), a scripted keystroke schedule, a Python terminal emulator (`pyte`), and the Python Imaging Library (`Pillow` / `PIL`).

The architecture works as follows:

```
┌──────────────┐  scripted keys   ┌──────────────────┐
│ generate_gif │ ───[pty I/O]───► │  middle-manager  │
│    script    │ ◄──[pty I/O]──── │  (wizard + TUI)  │
└──────────────┘                  └──────────────────┘
       │
    (parser)
       │
       ▼
┌──────────────┐    (renderer)    ┌──────────────┐
│  pyte cells  │ ───────────────► │ Pillow image │
└──────────────┘                  └──────────────┘
```

1. **Virtual Terminal Spawn:** We launch `mm --wizard` within a pseudo-terminal (`pty.openpty()`, 100×30 cells), so the binary runs its real interactive Bubble Tea wizard and monitor dashboard.
2. **Scripted Wizard Walkthrough:** A keystroke schedule — a list of `(seconds-from-start, bytes)` pairs — is replayed into the pty master. It accepts each prefilled wizard screen with Enter, types the mission character-by-character (35 ms per key, so it reads as human typing), briefly opens the per-seat agent carousel, walks the options checkboxes, and on the strength-ordering screen sends `j` then the `shift+↑` escape sequence (`\x1b[1;2A`) to visibly drag an agent up the ranking. Pauses between screens are tuned so a viewer can read each one.
3. **Mock Agent Commands:** `--binary agent=path/to/mock_agent.py` points **every builtin agent** at [mock_agent.py](scripts/mock_agent.py). Mocking the whole roster matters: the wizard defaults to `random` agents and a distinct verifier, so any agent can be rolled into any seat — a partially-mocked roster would let the demo invoke a real, token-burning CLI that happens to be installed on the host. The mock:
   - Identifies its step (Discover, Execute, Verify, or Commit) from the prompt text.
   - Streams realistic-looking console output line by line with natural delays.
   - Modifies `test-repo/main.py` in the execute step to add a Python docstring.
   - Emits a `VERDICT: PASS` during verification.
4. **Isolated Config/State:** The subprocess gets `XDG_CONFIG_HOME`/`XDG_STATE_HOME` pointed at a throwaway temp dir, so the demo never reads the host's real mm config (which would change the wizard's defaults) and never pollutes it (the wizard persists the strength ordering on launch).
5. **Capture and Parsing:** The terminal output stream is parsed in real time by a `pyte.Screen` emulator, tracking characters, bold attributes, the cursor, and the synthwave truecolor palette. (Bubble Tea v2's kitty-keyboard probe sequences are stripped first — pyte doesn't parse them.)
6. **Variable Frame-Rate Rendering:** At 100 ms intervals we inspect the screen. If it changed, we render the character grid onto a Pillow `Image` with monospace font tiles; if not, we extend the previous frame's duration. This compression keeps the file small (**~930 KB for ~30 s**) while preserving reading pauses. All frames are quantized against one shared 256-color palette so colors stay stable across the animation.
7. **Clean Ending:** When the dashboard prints "Press Enter to exit." the recorder holds that COMPLETED screen as a long final frame, then exits the TUI — recording the teardown would capture alt-screen restore garbage.

## Scripts

The source code for generating this demo is checked into the repository:
* **[mock_agent.py](scripts/mock_agent.py)**: Simulates the responses of the four pipeline agents.
* **[generate_gif.py](scripts/generate_gif.py)**: Builds the local `mm` binary, prepares an ignored `test-repo`, spawns the virtual terminal, replays the scripted wizard keystrokes, captures and renders frames, compresses duplicates, and outputs `docs/interface_demo.gif`.

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
This builds the local `mm` binary, replays the wizard walkthrough, runs the full 4-step loop simulation against an ignored local `test-repo` directory, and updates `docs/interface_demo.gif`. The run is fully deterministic-ish and free — no agent CLI is ever really invoked.

If you change the wizard's screen order or add a screen, update the keystroke schedule in `generate_gif.py` (the `events` list) to match, and re-check the timings by watching the output GIF.
