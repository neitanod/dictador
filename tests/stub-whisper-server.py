#!/usr/bin/env python3
"""Un whisper-server de mentira para el test end-to-end.

Contesta siempre el mismo texto, así el e2e mide el camino —tecla, micrófono,
motor, portapapeles, pegado— y no la calidad del reconocimiento, que se mide
aparte y con voz de verdad.
"""

import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

TEXT = sys.argv[2] if len(sys.argv) > 2 else "hola mundo"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def _reply(self, body: bytes, content_type="application/json"):
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        # El motor se fija en que la página se nombre antes de dar por bueno el
        # server: cualquier cosa puede estar ocupando ese puerto.
        self._reply(b"<html><title>whisper.cpp</title></html>", "text/html")

    def do_POST(self):
        size = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(size)
        # Que haya llegado un WAV es parte de lo que el test verifica.
        if b"RIFF" not in body:
            self.send_error(400, "no llegó un WAV")
            return
        self._reply(('{"text": "%s"}' % TEXT).encode())


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8099
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
