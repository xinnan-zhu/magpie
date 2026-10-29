# Run only inside the owning SSH session. No provider keys enter this process.
import json
import os
import select
import shutil
import signal
import socket
import socketserver
import stat
import sys
import threading

directory, port = sys.argv[1], int(sys.argv[2])
info = os.lstat(directory)
if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
    raise RuntimeError("SSH socket directory is not private")
path = os.path.join(directory, "gateway.sock")
info = os.lstat(path)
if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid():
    raise RuntimeError("SSH socket is unavailable")
os.chmod(path, 0o600)


class Relay(socketserver.BaseRequestHandler):
    def handle(self):
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as upstream:
            upstream.settimeout(5)
            upstream.connect(path)
            upstream.settimeout(None)
            streams = [self.request, upstream]
            while streams:
                ready, _, _ = select.select(streams, [], [], 1)
                for source in ready:
                    target = upstream if source is self.request else self.request
                    data = source.recv(65536)
                    if data:
                        target.sendall(data)
                    else:
                        streams.remove(source)
                        try:
                            target.shutdown(socket.SHUT_WR)
                        except OSError:
                            pass


class Server(socketserver.ThreadingTCPServer):
    daemon_threads = True
    allow_reuse_address = True


def stop(signum, frame):
    raise SystemExit(0)


signal.signal(signal.SIGHUP, stop)
signal.signal(signal.SIGTERM, stop)
try:
    with Server(("127.0.0.1", port), Relay) as server:
        threading.Thread(target=server.serve_forever, daemon=True).start()
        print(json.dumps({"ready": True, "port": server.server_address[1]}), flush=True)
        # EOF, including a lost SSH transport, owns this listener's lifetime.
        while sys.stdin.buffer.read(1024):
            pass
finally:
    shutil.rmtree(directory, ignore_errors=True)
