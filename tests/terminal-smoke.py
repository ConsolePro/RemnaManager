"""Exercise a real Linux PTY, F1 confirmation, resize, and terminal restoration."""
import fcntl
import os
import pty
import re
import select
import signal
import struct
import subprocess
import termios
import time

master, slave = pty.openpty()
before = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 80, 0, 0))


def child_setup():
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)


process = subprocess.Popen(
    ["/usr/local/bin/remanager"], stdin=slave, stdout=slave, stderr=slave,
    preexec_fn=child_setup, env={**os.environ, "TERM": "xterm-256color"},
)
output = bytearray()
answered_background = False
answered_cursor = False


def drain(seconds):
    global answered_background, answered_cursor
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if select.select([master], [], [], 0.05)[0]:
            output.extend(os.read(master, 65536))
            # A PTY is only a byte transport, not a terminal emulator. Answer
            # startup probes so Bubble Tea can initialize just as over SSH.
            if not answered_background and b"\x1b]11;?" in output:
                os.write(master, b"\x1b]11;rgb:0808/0e0e/1414\x1b\\")
                answered_background = True
            if not answered_cursor and b"\x1b[6n" in output:
                os.write(master, b"\x1b[1;1R")
                answered_cursor = True


try:
    for _ in range(24):
        drain(0.25)
        if b"REMNA MANAGER" in output:
            break
    assert b"REMNA MANAGER" in output, output.decode(errors="replace")
    drain(1.4)
    assert b"Uptime:" in output, "server uptime is missing from the header"
    timestamps = set(re.findall(rb"\d{2}\.\d{2}\.\d{4} \d{2}:\d{2}:\d{2}", output))
    assert len(timestamps) >= 2, "server clock did not update in a real PTY"
    os.write(master, b"\x1b[B\r\x1b")  # Open a section, then back.
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 60, 0, 0))
    os.kill(process.pid, signal.SIGWINCH)
    drain(0.3)
    os.write(master, b"\x1bOP")  # F1.
    drain(0.3)
    assert b"Exit?" in output
    os.write(master, b"\x1b[C\r")  # Select Yes and confirm.
    drain(0.5)
    process.wait(timeout=8)
    assert process.returncode == 0, process.returncode
    assert termios.tcgetattr(slave) == before, "terminal attributes not restored"
    assert b"\x1b[?1049l" in output, "alternate screen not restored"
    print("PTY live clock, uptime, navigation, F1, resize, terminal restoration: PASS")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
