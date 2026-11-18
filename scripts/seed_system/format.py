# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""The .seed file: reading it, what its keys may be, and its placeholders.

Nothing here talks to a server.
"""

import calendar
import datetime
import hashlib
import json
import re

FORMAT_VERSIONS = (1, 2)
SYSTEM_FIELDS = ("id", "_version")

# What a header may say. Version 1 knows the first three.
HEADER_KEYS_V1 = ("seed", "name", "tenant")
HEADER_KEYS = HEADER_KEYS_V1 + ("description", "tags", "version", "requires_xolu", "entities", "vars", "exclusive", "addition", "requires",
                                "notes", "x")
# Keys any record may carry, whatever its kind.
COMMON_KEYS = ("kind", "as", "bind", "phase", "expect", "x")
PLACEHOLDERS = ("$seed", "$date", "$now", "$var", "$text")
V2_PLACEHOLDERS = ("$date", "$now", "$var", "$text")


class SeedError(Exception):
    """Bad input: reported without a traceback, exit status 2."""


class SeedRefused(Exception):
    """A guard stopped the load before anything was written: exit status 3."""


def _where(path, line):
    return "%s:%d" % (path, line)


def read_seed(path, lenient=False, warn=None):
    """Return (header, records, digest) for one .seed file.

    A record is one JSON object. It starts in the first column and may continue
    on following lines that start with white space (version 2). Blank lines and
    lines starting with '#' separate records and are otherwise ignored. Each
    record gets "_file" and "_line".
    """
    warn = warn or (lambda message: None)
    try:
        with open(path, "rb") as fh:
            raw = fh.read()
    except OSError as exc:
        raise SeedError("cannot read %s: %s" % (path, exc))
    digest = hashlib.sha256(raw).hexdigest()
    try:
        text = raw.decode("utf-8-sig")
    except UnicodeDecodeError as exc:
        raise SeedError("%s: not UTF-8 text: %s" % (path, exc))

    chunks = []          # (first line number, text, spans several lines)
    for lineno, line in enumerate(text.splitlines(), 1):
        if not line.strip():
            continue
        if line[0] in " \t":
            if not chunks or chunks[-1][3]:
                raise SeedError("%s: a continuation line with no record before it" % _where(path, lineno))
            chunks[-1][1] += "\n" + line
            chunks[-1][2] = True
            continue
        if line.startswith("#"):
            if chunks:
                chunks[-1][3] = True        # a comment closes the record before it
            continue
        chunks.append([lineno, line, False, False])

    header, records, multiline = None, [], False
    for lineno, body, spans, _closed in chunks:
        try:
            obj = json.loads(body)
        except ValueError as exc:
            raise SeedError("%s: invalid JSON: %s" % (_where(path, lineno), exc))
        if not isinstance(obj, dict):
            raise SeedError("%s: each record must be a JSON object" % _where(path, lineno))
        multiline = multiline or spans
        if header is None:
            if "seed" not in obj:
                raise SeedError("%s: the first record must be the header, e.g. {\"seed\": 2, \"name\": \"demo\"}" % _where(path, lineno))
            if obj["seed"] not in FORMAT_VERSIONS or isinstance(obj["seed"], bool):
                raise SeedError("%s: unsupported seed format version %r (this loader reads versions %s)"
                                % (_where(path, lineno), obj["seed"], " and ".join(str(v) for v in FORMAT_VERSIONS)))
            header = obj
            header["_file"], header["_line"] = path, lineno
            continue
        if "kind" not in obj:
            raise SeedError("%s: record has no \"kind\"" % _where(path, lineno))
        obj["_file"], obj["_line"] = path, lineno
        records.append(obj)
    if header is None:
        raise SeedError("%s: empty seed file (no header record)" % path)

    version = header["seed"]
    allowed = HEADER_KEYS if version >= 2 else HEADER_KEYS_V1
    for key in header:
        if key.startswith("_") or key in allowed:
            continue
        hint = " (a version 2 key: declare \"seed\": 2)" if key in HEADER_KEYS else ""
        message = "%s: unknown header key \"%s\"%s" % (_where(path, header["_line"]), key, hint)
        if lenient:
            warn(message)
        else:
            raise SeedError(message)
    if version < 2 and multiline:
        raise SeedError("%s: records that span lines need \"seed\": 2 in the header" % path)
    _check_header_types(header, path)
    if version < 2:
        for rec in records:
            used = placeholders_in({k: v for k, v in rec.items() if not k.startswith("_")}) & set(V2_PLACEHOLDERS)
            if used:
                raise SeedError("%s: %s needs \"seed\": 2 in the header" % (_where(path, rec["_line"]), ", ".join(sorted(used))))
    return header, records, digest


def _check_header_types(header, path):
    where = _where(path, header["_line"])
    for key, kind, label in (("name", str, "a string"), ("tenant", str, "a string"), ("description", str, "a string"),
                             ("notes", str, "a string"), ("version", str, "a string"), ("requires_xolu", str, "a string"),
                             ("exclusive", bool, "true or false"), ("addition", bool, "true or false"), ("vars", dict, "an object"),
                             ("x", dict, "an object")):
        if key in header and not isinstance(header[key], kind):
            raise SeedError("%s: header \"%s\" must be %s" % (where, key, label))
    if header.get("exclusive") and (header.get("addition") or header.get("requires")):
        raise SeedError("%s: a whole dataset (\"exclusive\") cannot also be an addition to another" % where)
    for key in ("tags", "entities", "requires"):
        if key in header and not (isinstance(header[key], list) and all(isinstance(v, str) and v for v in header[key])):
            raise SeedError("%s: header \"%s\" must be a list of names" % (where, key))


def check_record_keys(rec, allowed, lenient=False, warn=None):
    """Refuse a key the record's kind does not define."""
    for key in rec:
        if key.startswith("_") or key in COMMON_KEYS or key in allowed:
            continue
        message = "%s:%d: unknown key \"%s\" on a '%s' record" % (rec["_file"], rec["_line"], key, rec["kind"])
        if lenient:
            if warn:
                warn(message)
        else:
            raise SeedError(message + " (known: %s)" % ", ".join(sorted(set(allowed) | set(COMMON_KEYS))))


def is_placeholder(value):
    return isinstance(value, dict) and len(value) == 1 and next(iter(value)) in PLACEHOLDERS


def placeholders_in(value):
    """The set of placeholder names ("$seed", "$date", ...) used anywhere in a value."""
    found = set()
    if is_placeholder(value):
        found.add(next(iter(value)))
    elif isinstance(value, dict):
        for v in value.values():
            found |= placeholders_in(v)
    elif isinstance(value, list):
        for v in value:
            found |= placeholders_in(v)
    return found


def bindings_in(value):
    """The names bound with "as" that a value refers to through {"$seed": name}."""
    found = set()
    if isinstance(value, dict):
        if len(value) == 1 and "$seed" in value:
            found.add(value["$seed"])
        elif len(value) == 1 and isinstance(value.get("$text"), str):
            found |= set(_SEED_IN_TEXT.findall(value["$text"]))
        else:
            for v in value.values():
                found |= bindings_in(v)
    elif isinstance(value, list):
        for v in value:
            found |= bindings_in(v)
    return found


_OFFSET = re.compile(r"^\s*(?:([+-])\s*(\d+)\s*(s|m|h|d|w|mo|y))?\s*$")
_DAY_TIME = re.compile(r"^(.*?)(T\d\d:\d\d:\d\dZ)$")
_IN_TEXT = re.compile(r"\$\{(date|now|var):([^}]*)\}")
_SEED_IN_TEXT = re.compile(r"\$\{seed:(\w+)\}")


def _shift(base, offset, where):
    """base (a datetime) moved by an offset such as "+90d", "-2h", "+1mo", "-3y" or ""."""
    m = _OFFSET.match(offset if isinstance(offset, str) else "?")
    if not m:
        raise SeedError("%s: \"%s\" is not an offset: use a sign, a whole number and one of s, m, h, d, w, mo, y "
                        "(for example \"+90d\", \"-2h\"), or \"\" for the load time itself" % (where, offset))
    if not m.group(1):
        return base
    n = int(m.group(2)) * (1 if m.group(1) == "+" else -1)
    unit = m.group(3)
    if unit in ("mo", "y"):
        months = base.year * 12 + (base.month - 1) + (n if unit == "mo" else 12 * n)
        year, month = divmod(months, 12)
        if not 1 <= year <= 9999:
            raise SeedError("%s: \"%s\" leaves the calendar" % (where, offset))
        day = min(base.day, calendar.monthrange(year, month + 1)[1])
        return base.replace(year=year, month=month + 1, day=day)
    try:
        return base + datetime.timedelta(**{{"s": "seconds", "m": "minutes", "h": "hours", "d": "days", "w": "weeks"}[unit]: n})
    except OverflowError:
        raise SeedError("%s: \"%s\" leaves the calendar" % (where, offset))


class Context:
    """What the placeholders of one load resolve against: the reference time and the variables."""

    def __init__(self, as_of=None, variables=None):
        self.as_of = as_of or datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0, tzinfo=None)
        self.variables = dict(variables or {})

    @staticmethod
    def parse_as_of(text):
        """A day (2026-11-17, taken at 00:00:00 UTC) or a time (2026-11-17T08:30:00Z)."""
        for pattern in ("%Y-%m-%dT%H:%M:%SZ", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%d"):
            try:
                return datetime.datetime.strptime(text, pattern)
            except ValueError:
                continue
        raise SeedError("--as-of: \"%s\" is neither a day (2026-11-17) nor a time (2026-11-17T08:30:00Z)" % text)

    def date(self, arg, where):
        """A day relative to the load; with a time of day after it ("-2dT08:30:00Z"), that time on that day."""
        m = _DAY_TIME.match(arg) if isinstance(arg, str) else None
        if m:
            return _shift(self.as_of, m.group(1), where).strftime("%Y-%m-%d") + m.group(2)
        return _shift(self.as_of, arg, where).strftime("%Y-%m-%d")

    def as_of_text(self):
        return self.as_of.strftime("%Y-%m-%dT%H:%M:%SZ")

    def resolve(self, value, where):
        """Replace {"$date"}, {"$now"}, {"$var"} and {"$text"} anywhere inside a value; leave {"$seed"} for the run."""
        if isinstance(value, dict):
            if len(value) == 1:
                key, arg = next(iter(value.items()))
                if key == "$date":
                    return self.date(arg, where)
                if key == "$text":
                    if not isinstance(arg, str):
                        raise SeedError("%s: {\"$text\"} takes a string" % where)
                    text = _IN_TEXT.sub(lambda m: str(self.resolve({"$" + m.group(1): m.group(2)}, where)), arg)
                    # a bound value inside the text is known only while loading: the rest is settled here
                    return {"$text": text} if _SEED_IN_TEXT.search(text) else text
                if key == "$now":
                    return _shift(self.as_of, arg, where).strftime("%Y-%m-%dT%H:%M:%SZ")
                if key == "$var":
                    if arg not in self.variables:
                        raise SeedError("%s: variable \"%s\" has no value: give it with --set %s=VALUE or a default "
                                        "in the header's \"vars\"" % (where, arg, arg))
                    return self.variables[arg]
            return {k: self.resolve(v, where) for k, v in value.items()}
        if isinstance(value, list):
            return [self.resolve(v, where) for v in value]
        return value


def _layout(value, indent, width):
    """A JSON value as text, broken over lines (each starting with white space) where it does not fit."""
    one = json.dumps(value, ensure_ascii=False)
    if len(one) + indent <= width or not isinstance(value, (dict, list)) or not value:
        return one
    pad = " " * (indent + 2)
    if isinstance(value, list):
        items = [pad + _layout(v, indent + 2, width) for v in value]
        return "[\n" + ",\n".join(items) + "\n" + " " * indent + "]"
    items = [pad + json.dumps(k, ensure_ascii=False) + ": " + _layout(v, indent + 2, width) for k, v in value.items()]
    return "{\n" + ",\n".join(items) + "\n" + " " * indent + "}"


def write_record(rec, width=110):
    """One record as text: on one line when it fits, otherwise its body laid out over indented lines."""
    one = json.dumps(rec, ensure_ascii=False)
    if "seed" in rec and "kind" not in rec and len(one) > width:        # a header: one key per line
        items = list(rec.items())
        first = json.dumps(dict(items[:1]), ensure_ascii=False)[:-1] + ","
        rest = ["  %s: %s%s" % (json.dumps(k, ensure_ascii=False), _layout(v, 2, width), "," if i < len(items) - 2 else "")
                for i, (k, v) in enumerate(items[1:])]
        return "\n".join([first] + rest + ["  }"])
    body_key = next((k for k in ("data", "body", "schema") if isinstance(rec.get(k), dict) and rec[k]), None)
    if len(one) <= width or body_key is None:
        return one
    head = {k: v for k, v in rec.items() if k != body_key}
    lines = [json.dumps(head, ensure_ascii=False)[:-1] + ", " + json.dumps(body_key) + ": {"]
    items = list(rec[body_key].items())
    for i, (k, v) in enumerate(items):
        key = json.dumps(k, ensure_ascii=False)
        lines.append("  %s: %s%s" % (key, _layout(v, 2, width), "," if i < len(items) - 1 else ""))
    lines.append("  }}")
    return "\n".join(lines)
