#!/usr/bin/env python3
import os
import sys
import pty
import select
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
                
            if bg_color != "#11111B":
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

def main():
    cols = 100
    rows = 30
    
    # Resolve paths relative to repository root
    repo_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
    target_repo = os.path.join(repo_root, "test-repo")
    mock_agent = os.path.join(repo_root, "docs", "scripts", "mock_agent.py")
    gif_path = os.path.join(repo_root, "interface_demo.gif")
    
    state_dir = os.path.join(target_repo, ".middle-manager")
    if os.path.exists(state_dir):
        subprocess.run(["rm", "-rf", state_dir])
        
    target_file = os.path.join(target_repo, "main.py")
    if os.path.exists(target_file):
        with open(target_file, "r") as f:
            lines = f.readlines()
        cleaned_lines = []
        in_doc = False
        for line in lines:
            if line.strip() == '"""' or line.strip().startswith('"""'):
                in_doc = not in_doc
                continue
            if in_doc:
                continue
            cleaned_lines.append(line)
        with open(target_file, "w") as f:
            f.writelines(cleaned_lines)
            
    subprocess.run(["git", "checkout", "main.py"], cwd=target_repo)
    subprocess.run(["git", "reset", "--hard"], cwd=target_repo)
    subprocess.run(["git", "checkout", "master"], cwd=target_repo)
    subprocess.run(["git", "branch", "-D", "mm/loop-1"], cwd=target_repo)

    screen = pyte.Screen(cols, rows)
    stream = pyte.Stream(screen)
    
    # Locate fonts
    font_path_reg = "/usr/share/fonts/liberation-mono-fonts/LiberationMono-Regular.ttf"
    font_path_bold = "/usr/share/fonts/liberation-mono-fonts/LiberationMono-Bold.ttf"
    
    if not os.path.exists(font_path_reg):
        # System fallback search
        font_path_reg = "DejaVuSansMono.ttf"  # Try fallback
        font_path_bold = "DejaVuSansMono-Bold.ttf"
        
    try:
        font_reg = ImageFont.truetype(font_path_reg, 15)
        font_bold = ImageFont.truetype(font_path_bold, 15)
    except IOError:
        # Fallback to default PIL font
        font_reg = ImageFont.load_default()
        font_bold = ImageFont.load_default()
    
    cmd = [
        "./mm", "--mission", "Add a simple docstring to main.py",
        "--repo", target_repo,
        "--no-wizard",
        "--no-pr",
        f"--binary=claude={mock_agent}",
        f"--binary=opencode={mock_agent}",
        f"--binary=grok={mock_agent}"
    ]
    
    master_fd, slave_fd = pty.openpty()
    size = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(slave_fd, termios.TIOCSWINSZ, size)
    
    p = subprocess.Popen(cmd, stdin=slave_fd, stdout=slave_fd, stderr=slave_fd, close_fds=True, cwd=repo_root)
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
        frames[0].save(
            gif_path,
            save_all=True,
            append_images=frames[1:],
            duration=durations,
            loop=0
        )
        print(f"Successfully saved animated GIF with {len(frames)} frames to {gif_path}")
    else:
        print("No frames captured.")

if __name__ == "__main__":
    main()
