#!/usr/bin/env python3
import os
import sys
import pty
import select
import shutil
import subprocess
import time
import struct
import fcntl
import termios
import pyte
from PIL import Image, ImageDraw, ImageFont

COLOR_MAP = {
    "default": "#11111B",
    "black": "#11111B",
    "red": "#FF5C72",
    "green": "#3DF5A0",
    "yellow": "#FFC857",
    "blue": "#9D7CFF",  # cViolet
    "magenta": "#FF5FD7",
    "cyan": "#36E2E2",
    "white": "#E6E6F0",
    "lightgray": "#E6E6F0",
    "darkgray": "#6C7086",
}

def get_color(color_name, is_bg=False):
    if color_name is None:
        return "#11111B" if is_bg else "#E6E6F0"
    color_name = color_name.lower()
    if len(color_name) == 6:
        return f"#{color_name}"
    if color_name.startswith("#"):
        return color_name
    if color_name == "default":
        return "#11111B" if is_bg else "#E6E6F0"
    return COLOR_MAP.get(color_name, "#11111B" if is_bg else "#E6E6F0")

def render_screen_to_image(screen, font_reg, font_bold):
    cell_width = 9
    cell_height = 18
    
    cols = screen.columns
    rows = screen.lines
    
    img = Image.new("RGB", (cols * cell_width, rows * cell_height), "#11111B")
    draw = ImageDraw.Draw(img)
    
    for y in range(rows):
        for x in range(cols):
            cell = screen.buffer[y][x]
            bg_color = get_color(cell.bg, is_bg=True)
            fg_color = get_color(cell.fg, is_bg=False)
            
            if cell.reverse:
                fg_color, bg_color = bg_color, fg_color
                
            if bg_color.lower() != "#11111b":
                draw.rectangle(
                    [x * cell_width, y * cell_height, (x + 1) * cell_width - 1, (y + 1) * cell_height - 1],
                    fill=bg_color
                )
            
            if not screen.cursor.hidden and screen.cursor.x == x and screen.cursor.y == y:
                draw.rectangle(
                    [x * cell_width, y * cell_height, (x + 1) * cell_width - 1, (y + 1) * cell_height - 1],
                    outline="#FF5FD7", width=1
                )
                
            char = cell.data
            if char and char != " ":
                font = font_bold if cell.bold else font_reg
                draw.text((x * cell_width, y * cell_height + 1), char, font=font, fill=fg_color)
                
    return img

def run_quiet(args, cwd=None, check=True):
    return subprocess.run(
        args,
        cwd=cwd,
        check=check,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )

def write_file(path, content):
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)

def prepare_demo_repo(target_repo):
    if os.path.exists(target_repo):
        shutil.rmtree(target_repo)

    os.makedirs(target_repo, exist_ok=True)
    run_quiet(["git", "init", "-b", "main"], cwd=target_repo)
    run_quiet(["git", "config", "user.email", "demo@example.com"], cwd=target_repo)
    run_quiet(["git", "config", "user.name", "Demo User"], cwd=target_repo)
    write_file(
        os.path.join(target_repo, "main.py"),
        """import json


def load_settings(path="settings.json"):
    try:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return {"debug": False, "port": 8080}


def main():
    settings = load_settings()
    print(f"server listening on :{settings['port']}")


if __name__ == "__main__":
    main()
""",
    )
    write_file(
        os.path.join(target_repo, "AGENTS.md"),
        "# AGENTS.md\n\nRun `python3 -m py_compile main.py` for quick syntax checks.\n",
    )
    run_quiet(["git", "add", "main.py", "AGENTS.md"], cwd=target_repo)
    run_quiet(["git", "commit", "-m", "initial demo repo"], cwd=target_repo)

def main():
    cols = 100
    rows = 30
    
    # Resolve paths relative to repository root
    repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
    target_repo = os.path.join(repo_root, "test-repo")
    mock_agent = os.path.join(repo_root, "docs", "scripts", "mock_agent.py")
    gif_path = os.path.join(repo_root, "docs", "interface_demo.gif")

    run_quiet(["go", "build", "-o", "mm", "."], cwd=repo_root)
    prepare_demo_repo(target_repo)

    screen = pyte.Screen(cols, rows)
    stream = pyte.Stream(screen)
    
    font_pairs = [
        (
            "/usr/share/fonts/adwaita-mono-fonts/AdwaitaMono-Regular.ttf",
            "/usr/share/fonts/adwaita-mono-fonts/AdwaitaMono-Bold.ttf",
        ),
        (
            "/usr/share/fonts/liberation-mono-fonts/LiberationMono-Regular.ttf",
            "/usr/share/fonts/liberation-mono-fonts/LiberationMono-Bold.ttf",
        ),
        (
            "/usr/share/fonts/google-noto-vf/NotoSansMono[wght].ttf",
            "/usr/share/fonts/google-noto-vf/NotoSansMono[wght].ttf",
        ),
    ]

    font_reg = font_bold = None
    for font_path_reg, font_path_bold in font_pairs:
        if os.path.exists(font_path_reg) and os.path.exists(font_path_bold):
            font_reg = ImageFont.truetype(font_path_reg, 15)
            font_bold = ImageFont.truetype(font_path_bold, 15)
            break
    if font_reg is None:
        raise RuntimeError("No usable monospace font found for GIF rendering")
    
    cmd = [
        "./mm", "--mission", "Add a simple module docstring to main.py",
        "--repo", target_repo,
        "--no-wizard",
        "--no-pr",
        "--binary", f"claude={mock_agent}",
        "--binary", f"opencode={mock_agent}",
        "--binary", f"grok={mock_agent}"
    ]
    
    master_fd, slave_fd = pty.openpty()
    size = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(slave_fd, termios.TIOCSWINSZ, size)
    
    env = os.environ.copy()
    env["TERM"] = "xterm-256color"
    env["COLORTERM"] = "truecolor"
    
    p = subprocess.Popen(cmd, stdin=slave_fd, stdout=slave_fd, stderr=slave_fd, close_fds=True, cwd=repo_root, env=env)
    os.close(slave_fd)
    
    frames = []
    durations = []
    
    last_text_hash = None
    interval = 0.1
    start_time = time.time()
    
    while p.poll() is None:
        loop_start = time.time()
        
        r, w, x = select.select([master_fd], [], [], 0.05)
        if master_fd in r:
            try:
                data = os.read(master_fd, 4096)
                if data:
                    stream.feed(data.decode("utf-8", errors="ignore"))
            except OSError:
                pass
        
        current_text = "\n".join(screen.display)
        if "Press Enter to exit." in current_text:
            os.write(master_fd, b"\r")
            time.sleep(0.5)
        
        if last_text_hash is None or current_text != last_text_hash:
            img = render_screen_to_image(screen, font_reg, font_bold)
            frames.append(img)
            durations.append(int(interval * 1000))
            last_text_hash = current_text
        else:
            if durations:
                durations[-1] += int(interval * 1000)
                
        elapsed = time.time() - loop_start
        sleep_time = max(0, interval - elapsed)
        time.sleep(sleep_time)
        
        if time.time() - start_time > 60:
            print("Recording timed out.")
            p.terminate()
            break
            
    time.sleep(0.5)
    r, w, x = select.select([master_fd], [], [], 0.1)
    if master_fd in r:
        try:
            data = os.read(master_fd, 4096)
            if data:
                stream.feed(data.decode("utf-8", errors="ignore"))
        except OSError:
            pass
            
    img = render_screen_to_image(screen, font_reg, font_bold)
    frames.append(img)
    durations.append(4000)
    
    os.close(master_fd)
    
    if frames:
        # Combine all frames into a single image to generate a unified palette
        width, height = frames[0].size
        combined = Image.new("RGB", (width, height * len(frames)))
        for idx, frame in enumerate(frames):
            combined.paste(frame, (0, idx * height))
        
        # Quantize the combined image to 256 colors
        palette_image = combined.quantize(colors=256, method=Image.Quantize.MAXCOVERAGE)
        
        # Convert each individual frame using the unified palette
        p_frames = [frame.quantize(palette=palette_image, dither=Image.Dither.NONE) for frame in frames]
        
        p_frames[0].save(
            gif_path,
            save_all=True,
            append_images=p_frames[1:],
            duration=durations,
            loop=0
        )
        print(f"Successfully saved animated GIF with {len(frames)} frames to {gif_path}")
    else:
        print("No frames captured.")

if __name__ == "__main__":
    main()
