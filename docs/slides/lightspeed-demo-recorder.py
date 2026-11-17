#!/usr/bin/env python3
"""Demo-only fixed-upstream request recorder; never records payloads or credentials."""

import argparse
import contextlib
import datetime
import http.client
import io
import json
import ssl
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

HOP = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
    "host",
}


def clean_headers(headers):
    excluded = HOP | {
        part.strip().lower() for part in headers.get("Connection", "").split(",")
    }
    return {key: value for key, value in headers.items() if key.lower() not in excluded}


def request_class(method, path, body, classifier):
    if method != "POST" or path != "/v1/messages":
        return "other"
    try:
        system = json.loads(body).get("system")
        if (
            isinstance(system, list)
            and len(system) == 1
            and system[0].get("type") == "text"
        ):
            system = system[0].get("text")
        return "classifier" if system == classifier else "non_classifier"
    except (ValueError, UnicodeError, AttributeError, TypeError):
        return "non_classifier"


class Audit:
    def __init__(self, output):
        self.output, self.lock, self.sequence = output, threading.Lock(), 0

    def emit(self, event, **metadata):
        with self.lock:
            self.sequence += 1
            record = dict(
                seq=self.sequence,
                utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                monotonic_ns=time.monotonic_ns(),
                event=event,
                **metadata,
            )
            self.output.write(json.dumps(record) + "\n")
            self.output.flush()


def proxy_handler(upstream, classifier, audit, timeout=3660):
    target = urlsplit(upstream)

    class Proxy(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args):
            pass

        def forward(self):
            metadata = {
                "request_id": uuid.uuid4().hex,
                "method": self.command,
                "path": urlsplit(self.path).path,
                "request_class": "other",
            }
            connection, status = None, None
            started = False
            self.close_connection = True
            try:
                if not self.path.startswith("/") or self.path.startswith("//"):
                    raise ValueError("origin-form path required")
                if self.headers.get("Transfer-Encoding"):
                    raise ValueError("chunked request bodies unsupported")
                length = int(self.headers.get("Content-Length", "0"))
                if length < 0:
                    raise ValueError("negative content length")
                body = self.rfile.read(length)
                if len(body) != length:
                    raise ValueError("incomplete request")
                metadata["request_class"] = request_class(
                    self.command, metadata["path"], body, classifier
                )
                audit.emit("start", **metadata)
                started = True
                if target.scheme == "https":
                    connection = http.client.HTTPSConnection(
                        target.hostname,
                        target.port,
                        context=ssl.create_default_context(),
                        timeout=timeout,
                    )
                else:  # Only the local self-test uses HTTP; CLI requires HTTPS.
                    connection = http.client.HTTPConnection(
                        target.hostname, target.port, timeout=10
                    )
                connection.request(
                    self.command,
                    target.path.rstrip("/") + self.path,
                    body=body,
                    headers=clean_headers(self.headers),
                )
                response = connection.getresponse()
                status = response.status
                audit.emit("status", status=status, **metadata)
                self.send_response_only(status, response.reason)
                for key, value in clean_headers(response.headers).items():
                    self.send_header(key, value)
                self.send_header("Connection", "close")
                self.end_headers()
                while chunk := response.read1(65536):
                    self.wfile.write(chunk)
                    self.wfile.flush()
                if response.length not in (None, 0):
                    raise http.client.IncompleteRead(b"")
                audit.emit("end", status=status, outcome="complete", **metadata)
            except (OSError, http.client.HTTPException, ValueError) as error:
                # Close a failed transport; never invent a provider response or log details.
                audit.emit(
                    "end",
                    status=status,
                    outcome="transport_error",
                    error_type=type(error).__name__,
                    started=started,
                    **metadata,
                )
            finally:
                if connection is not None:
                    connection.close()

    Proxy.do_GET = Proxy.do_POST = Proxy.forward
    return Proxy


def self_test():
    import unittest

    classifier = "SELFTEST CLASSIFIER CONTRACT"
    first, last = b"data: SSE-FIRST-CANARY\n\n", b"data: SSE-LAST-CANARY\n\n"

    @contextlib.contextmanager
    def servers(status=200):
        observed, release, output = [], threading.Event(), io.StringIO()

        class Upstream(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_POST(self):
                observed.append(
                    (
                        self.path,
                        dict(self.headers),
                        self.rfile.read(int(self.headers["Content-Length"])),
                    )
                )
                self.send_response(status)
                if status == 401:
                    content = b'{"error":"RAW-ERROR-CANARY"}'
                    self.send_header("Content-Length", str(len(content)))
                    self.end_headers()
                    self.wfile.write(content)
                else:
                    self.send_header("Content-Type", "text/event-stream")
                    self.send_header("Transfer-Encoding", "chunked")
                    self.end_headers()
                    for chunk in (first, last):
                        self.wfile.write(
                            f"{len(chunk):x}\r\n".encode() + chunk + b"\r\n"
                        )
                        self.wfile.flush()
                        if chunk == first:
                            release.wait(5)
                    self.wfile.write(b"0\r\n\r\n")

            do_GET = do_POST

        upstream = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        proxy = ThreadingHTTPServer(
            ("127.0.0.1", 0),
            proxy_handler(
                f"http://127.0.0.1:{upstream.server_port}", classifier, Audit(output)
            ),
        )
        for server in (upstream, proxy):
            threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            yield proxy.server_port, observed, release, output
        finally:
            release.set()
            for server in (proxy, upstream):
                server.shutdown()
                server.server_close()

    class Tests(unittest.TestCase):
        def test_exact_classification(self):
            for system in (classifier, [{"type": "text", "text": classifier}]):
                self.assertEqual(
                    request_class(
                        "POST",
                        "/v1/messages",
                        json.dumps({"system": system}),
                        classifier,
                    ),
                    "classifier",
                )
            for system in (
                classifier + " extra",
                [{"type": "text", "text": classifier}] * 2,
                None,
            ):
                self.assertEqual(
                    request_class(
                        "POST",
                        "/v1/messages",
                        json.dumps({"system": system}),
                        classifier,
                    ),
                    "non_classifier",
                )
            self.assertEqual(
                request_class("GET", "/v1/models", b"", classifier), "other"
            )

        def test_auth_body_and_incremental_sse(self):
            body = json.dumps(
                {
                    "system": classifier,
                    "messages": [{"role": "user", "content": "BODY-CANARY"}],
                }
            ).encode()
            with servers() as (port, observed, release, output):
                client = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
                client.request(
                    "POST",
                    "/v1/messages?hidden=QUERY-CANARY",
                    body,
                    {
                        "Authorization": "Bearer AUTH-CANARY",
                        "x-api-key": "KEY-CANARY",
                        "Connection": "close, X-Hop",
                        "X-Hop": "DROP-CANARY",
                        "Host": "untrusted.example",
                    },
                )
                response = client.getresponse()
                self.assertEqual(response.status, 200)
                self.assertEqual(
                    response.read1(65536), first
                )  # Arrives before upstream finishes.
                release.set()
                self.assertEqual(response.read(), last)
                client.close()
                self.assertEqual(observed[0][2], body)
                headers = {key.lower(): value for key, value in observed[0][1].items()}
                self.assertEqual(headers["authorization"], "Bearer AUTH-CANARY")
                self.assertEqual(headers["x-api-key"], "KEY-CANARY")
                self.assertNotIn("x-hop", headers)
                self.assertNotEqual(headers["host"], "untrusted.example")
                records = [json.loads(line) for line in output.getvalue().splitlines()]
                self.assertEqual(
                    [r["event"] for r in records], ["start", "status", "end"]
                )
                self.assertEqual([r["seq"] for r in records], [1, 2, 3])
                self.assertTrue(
                    all(r["request_class"] == "classifier" for r in records)
                )
                self.assertNotIn("CANARY", output.getvalue())

        def test_raw_401_passthrough(self):
            with servers(401) as (port, observed, _release, output):
                client = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
                for method, path, kind in (
                    ("POST", "/v1/messages", "non_classifier"),
                    ("GET", "/v1/models", "other"),
                ):
                    client.request(method, path, b'{"system":"normal"}')
                    response = client.getresponse()
                    self.assertEqual(response.status, 401)
                    self.assertEqual(response.read(), b'{"error":"RAW-ERROR-CANARY"}')
                    client.close()
                    self.assertEqual(observed[-1][0], path)
                    record = json.loads(output.getvalue().splitlines()[-1])
                    self.assertEqual(
                        (record["status"], record["request_class"]), (401, kind)
                    )
                self.assertNotIn("CANARY", output.getvalue())

    return (
        unittest.TextTestRunner(verbosity=2)
        .run(unittest.defaultTestLoader.loadTestsFromTestCase(Tests))
        .wasSuccessful()
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--upstream", default="https://api.enmaas.devshift.net")
    parser.add_argument("--listen", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument(
        "--timeout", type=int, default=3660, help="upstream socket timeout in seconds"
    )
    args = parser.parse_args()
    if args.self_test:
        return 0 if self_test() else 1
    if args.timeout <= 0:
        parser.error("timeout must be positive")
    upstream = urlsplit(args.upstream)
    if (
        upstream.scheme != "https"
        or not upstream.hostname
        or upstream.username
        or upstream.password
        or upstream.query
        or upstream.fragment
    ):
        parser.error(
            "upstream must be a fixed HTTPS base URL without credentials, query, or fragment"
        )
    from lightspeed_agentic.inspection.client import CLASSIFIER_SYSTEM_INSTRUCTION

    server = ThreadingHTTPServer(
        (args.listen, args.port),
        proxy_handler(
            args.upstream,
            CLASSIFIER_SYSTEM_INSTRUCTION,
            Audit(sys.stdout),
            args.timeout,
        ),
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
