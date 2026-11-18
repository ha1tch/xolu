# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""A small HTTP client for xolu that keeps its connection open."""

import http.client
import json
import os
import time
import urllib.parse

from .format import SeedError

DEFAULT_PORT = 9090


def default_base_url():
    """XOLU_BASE_URL, else XOLU_HOST / XOLU_PORT / XOLU_ADDR, else http://127.0.0.1:9090."""
    explicit = os.environ.get("XOLU_BASE_URL")
    if explicit:
        return explicit.rstrip("/")
    addr = os.environ.get("XOLU_ADDR", "")
    addr_host, addr_port = "", ""
    if ":" in addr:
        addr_host, _, addr_port = addr.rpartition(":")
    host = os.environ.get("XOLU_HOST") or addr_host or "127.0.0.1"
    port = os.environ.get("XOLU_PORT") or addr_port or str(DEFAULT_PORT)
    return "http://%s:%s" % (host, port)


class Client:
    def __init__(self, base, api_key=None, token=None, timeout=30.0):
        self.base = base.rstrip("/")
        parts = urllib.parse.urlsplit(self.base)
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise SeedError("\"%s\" is not a base URL (expected http://host:port)" % base)
        self._parts, self._prefix, self.timeout, self._conn = parts, parts.path.rstrip("/"), timeout, None
        self.headers = {}
        if api_key:
            self.headers["X-API-Key"] = api_key
        if token:
            self.headers["Authorization"] = "Bearer " + token
        self.requests = 0

    def _connect(self):
        cls = http.client.HTTPSConnection if self._parts.scheme == "https" else http.client.HTTPConnection
        return cls(self._parts.hostname, self._parts.port, timeout=self.timeout)

    def close(self):
        if self._conn is not None:
            self._conn.close()
            self._conn = None

    def request(self, method, path, body=None, raw=None, headers=None, query=None):
        """Return (status, parsed body). A connection that went away is opened again once."""
        target = self._prefix + path
        if query:
            target += "?" + urllib.parse.urlencode(query, doseq=True)
        data = None
        hdrs = dict(self.headers)
        if raw is not None:
            data = raw
        elif body is not None:
            data = json.dumps(body).encode("utf-8")
            hdrs["Content-Type"] = "application/json"
        if headers:
            hdrs.update(headers)
        last = None
        for attempt in (1, 2):
            try:
                if self._conn is None:
                    self._conn = self._connect()
                self._conn.request(method, target, body=data, headers=hdrs)
                resp = self._conn.getresponse()
                status, payload = resp.status, resp.read()
                break
            except (http.client.HTTPException, OSError) as exc:
                last = exc
                self.close()
                # a request that may have reached the server is not sent twice, unless it only reads
                if attempt == 2 or (method not in ("GET", "HEAD") and not isinstance(exc, (ConnectionRefusedError, http.client.RemoteDisconnected, BrokenPipeError, ConnectionResetError))):
                    raise SeedError("%s %s%s: %s" % (method, self.base, path, exc))
        else:
            raise SeedError("%s %s%s: %s" % (method, self.base, path, last))
        self.requests += 1
        parsed = None
        if payload:
            text = payload.decode("utf-8", "replace")
            try:
                parsed = json.loads(text)
            except ValueError:
                parsed = text
        return status, parsed

    def wait_ready(self, seconds):
        deadline = time.time() + seconds
        last = None
        while True:
            try:
                status, _ = self.request("GET", "/health")
                if 200 <= status < 300:
                    return
                last = "HTTP %d" % status
            except SeedError as exc:
                last = str(exc)
            if time.time() >= deadline:
                raise SeedError("xolu not ready at %s after %ss (%s)" % (self.base, seconds, last))
            time.sleep(0.5)
