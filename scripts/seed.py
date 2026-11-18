#!/usr/bin/env python3
# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""seed.py -- load, check and dump .seed files for xolu.

    python3 scripts/seed.py list
    python3 scripts/seed.py load example --url http://localhost:9090
    python3 scripts/seed.py check example --url http://localhost:9090
    python3 scripts/seed.py dump --url http://localhost:9090 -o my.seed
    python3 scripts/seed.py COMMAND --help

Seeds named without a path are looked for in <repo>/seeds (XOLU_SEEDS_DIR, or
--seeds-dir, to look elsewhere). scripts/seed.sh is the same command with the
habits of the loader it replaces: with no command it loads.

Python 3.8 or later, standard library only. The format, versions 1 and 2, is
specified in docs/SEED_FORMAT.md; the code is the seed_system package beside
this file, which Seam AMS uses as well; scripts/seed_tests and
scripts/seed_conformance hold it to the specification.
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
os.environ.setdefault("XOLU_SEEDS_DIR", os.path.join(os.path.dirname(HERE), "seeds"))
sys.path.insert(0, HERE)
from seed_system.cli import main  # noqa: E402

if __name__ == "__main__":
    sys.exit(main())
