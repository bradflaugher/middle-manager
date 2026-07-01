#!/usr/bin/env python3
import sys
import os
import time

def main():
    prompt = ""
    for i, arg in enumerate(sys.argv):
        if arg == "-p" and i + 1 < len(sys.argv):
            prompt = sys.argv[i + 1]
            break
    
    if not prompt:
        for arg in sys.argv[1:]:
            if not arg.startswith("-"):
                prompt = arg
                break

    if "Planner" in prompt or "Discover" in prompt or "discover" in sys.argv[0]:
        run_discover()
    elif "Programmer" in prompt or "Generate" in prompt or "opencode" in sys.argv[0]:
        run_execute()
    elif "Critic" in prompt or "Audit" in prompt or "verify" in sys.argv[0]:
        run_verify()
    elif "Ship" in prompt or "Commit" in prompt or "grok" in sys.argv[0]:
        run_commit()
    else:
        prompt_lower = prompt.lower()
        if "planning" in prompt_lower or "planner" in prompt_lower or "discover" in prompt_lower:
            run_discover()
        elif "programmer" in prompt_lower or "generate" in prompt_lower or "execute" in prompt_lower:
            run_execute()
        elif "critic" in prompt_lower or "audit" in prompt_lower or "verify" in prompt_lower:
            run_verify()
        else:
            argv_str = " ".join(sys.argv).lower()
            if "discover" in argv_str:
                run_discover()
            elif "execute" in argv_str or "opencode" in argv_str:
                run_execute()
            elif "verify" in argv_str:
                run_verify()
            else:
                run_commit()

def stream_print(text, delay=0.18, final_sleep=1.5):
    for line in text.splitlines(keepends=True):
        sys.stdout.write(line)
        sys.stdout.flush()
        time.sleep(delay)
    time.sleep(final_sleep)

def run_discover():
    output = """[INFO] Initializing Claude Code agent...
[INFO] Scanning workspace directory...
[THINK] The user wants to: Add a simple docstring to main.py.
[THINK] Let's read main.py to understand its current content.
[INFO] Reading main.py...
[THINK] The file main.py loads JSON settings with safe built-in defaults.
[THINK] It does not have any module-level docstring.
[THINK] We need to add a module-level docstring at the top of the file.
[PLAN] Files to modify:
  - main.py: Add top-level docstring.
[PLAN] Guidelines:
  - Insert a triple-quoted Python docstring at line 1.
  - Explain that the script loads settings from JSON with safe defaults.
[INFO] Completed discovery phase. Scoped 1 file.
"""
    stream_print(output)

def run_execute():
    output = """[INFO] Initializing OpenCode agent...
[THINK] Target: Add a docstring to main.py based on discovery guidelines.
[THINK] Let's read main.py first.
[THINK] Now, let's write the modified main.py with the docstring:
\"\"\"
Middle Manager Test Repo
This script loads JSON settings and falls back to safe defaults.
\"\"\"
[INFO] Successfully wrote updated content to main.py.
[THINK] Let's verify by running a quick syntax check.
[INFO] Running: python3 -m py_compile main.py
[INFO] Syntax check passed.
[INFO] Execution step completed successfully.
"""
    stream_print(output)
    
    target_file = "main.py"
    if os.path.exists(target_file):
        with open(target_file, "r") as f:
            content = f.read()
        if '"""' not in content:
            docstring = '"""\nMiddle Manager Test Repo\nThis script loads JSON settings and falls back to safe defaults.\n"""\n'
            with open(target_file, "w") as f:
                f.write(docstring + content)

def run_verify():
    output = """[INFO] Initializing Critic agent...
[THINK] Evaluating changes made in main.py.
[INFO] Running git diff main.py:
+ \"\"\"
+ Middle Manager Test Repo
+ This script loads JSON settings and falls back to safe defaults.
+ \"\"\"
[THINK] Docstring looks correct and accurate.
[INFO] Running test suite...
[INFO] Tests passed: 1/1 tests successful.
VERDICT: PASS
SUMMARY: Module-level docstring successfully added to main.py.
"""
    stream_print(output)

def run_commit():
    # Mirrors the real commit contract: learnings go to the orchestrator notes
    # file OUTSIDE the repo; AGENTS.md is never touched by the loop.
    output = """[INFO] Initializing Grok Ship agent...
[THINK] The verification passed. Persist learnings, then land one clean commit.
[INFO] Appending learnings to orchestrator notes (outside the repo)...
[INFO] Staging files...
[INFO] Running: git add main.py
[INFO] Running: git commit -m "middle-manager: Add a simple docstring to main.py"
[INFO] Commit successful.
"""
    stream_print(output)

if __name__ == "__main__":
    main()
