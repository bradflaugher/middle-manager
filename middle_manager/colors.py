"""Centralized ANSI terminal color definitions."""

from __future__ import annotations
import sys

class Colors:
    CYAN = "\033[96m"
    GREEN = "\033[92m"
    YELLOW = "\033[93m"
    RED = "\033[91m"
    MAGENTA = "\033[95m"
    BOLD = "\033[1m"
    RESET = "\033[0m"

    @classmethod
    def colored(cls, text: str, color: str) -> str:
        if sys.stdout.isatty():
            return f"{color}{text}{cls.RESET}"
        return text
