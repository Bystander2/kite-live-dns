"""Vercel HTTP bridge. All x402 processing is performed by the Rust binary."""
import http.client
import os
from pathlib import Path
import subprocess
import shutil
import threading
import time
from http.server import BaseHTTPRequestHandler

ROOT = Path(__file__).resolve().parents[1]
_process = None
_lock = threading.Lock()

def start_rust():
    global _process
    with _lock:
        if _process is None or _process.poll() is not None:
            source = ROOT / 'bin' / 'kite-live-dns-axum'
            binary = Path('/tmp/kite-live-dns-axum')
            shutil.copyfile(source, binary)
            binary.chmod(0o755)
            config = dict(os.environ)
            config.update(PORT='8090')
            config.setdefault('PAY_TO', '0xa5d1f687b741af9b2b7c2b0d77757c6a0de69055')
            config.setdefault('UPSTREAM_URL', 'https://dns.google')
            config.setdefault('KITE_NETWORK', 'testnet')
            config.setdefault('PRICE_USD', '0.001')
            _process = subprocess.Popen([str(binary)], env=config)
        for _ in range(40):
            conn = http.client.HTTPConnection('127.0.0.1', 8090, timeout=1)
            try:
                conn.request('GET', '/healthz')
                if conn.getresponse().status == 200:
                    return
            except OSError:
                if _process.poll() is not None:
                    raise RuntimeError('Rust process failed to start')
                time.sleep(0.05)
            finally:
                conn.close()
        raise RuntimeError('Rust startup timeout')

class handler(BaseHTTPRequestHandler):
    def handle_request(self):
        if self.path.split('?')[0] in ('/', '/payment-test.html'):
            data = (ROOT / 'public' / 'payment-test.html').read_bytes()
            self.send_response(200)
            self.send_header('Content-Type', 'text/html; charset=utf-8')
            self.end_headers()
            if self.command != 'HEAD': self.wfile.write(data)
            return
        conn = None
        try:
            start_rust()
            length = int(self.headers.get('Content-Length', '0'))
            if length > 2 * 1024 * 1024:
                self.send_error(413)
                return
            body = self.rfile.read(length) if length else None
            headers = {k:v for k,v in self.headers.items() if k.lower() not in ('connection','transfer-encoding','content-length')}
            conn = http.client.HTTPConnection('127.0.0.1',8090,timeout=55)
            # This deployment is HTTPS-only. Absolute URI preserves the public scheme
            # without trusting a buyer-controlled X-Forwarded-Proto header.
            host = self.headers.get('Host', '')
            if not host or any(c in host for c in '/?#@ \r\n'):
                self.send_error(400, 'Invalid Host')
                return
            conn.request(self.command,'https://' + host + self.path,body=body,headers=headers)
            upstream = conn.getresponse()
            data = upstream.read()
            self.send_response(upstream.status)
            for k,v in upstream.getheaders():
                if k.lower() not in ('connection','transfer-encoding','content-length'): self.send_header(k,v)
            self.end_headers()
            if self.command != 'HEAD': self.wfile.write(data)
        except Exception as exc:
            print('Rust bridge error:', type(exc).__name__, str(exc))
            self.send_error(502, 'Rust backend unavailable')
        finally:
            if conn: conn.close()
    do_GET = handle_request
    do_HEAD = handle_request
    do_POST = handle_request
    do_PUT = handle_request
    do_PATCH = handle_request
    do_DELETE = handle_request

    do_OPTIONS = handle_request
    do_TRACE = handle_request
