# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0
# https://www.apache.org/licenses/LICENSE-2.0
"""seed_system: read, check, load and dump .seed files for xolu.

A .seed file is UTF-8 text holding JSON records: a header, then one record per
thing to create. The format is specified in docs/SEED_FORMAT.md; this package
reads versions 1 and 2 of it. Python standard library only.

    from seed_system import read_seed, build_plan, Loader

Command line: python3 scripts/seed.py --help
"""

from .format import FORMAT_VERSIONS, SeedError, SeedRefused, read_seed, write_record, Context
from .plan import build_plan, KINDS
from .client import Client
from .load import Loader, PROVENANCE_ENTITY
from .inspect import check_seed, dump_instance

__version__ = "1.0.0"
__all__ = ["FORMAT_VERSIONS", "SeedError", "SeedRefused", "read_seed", "write_record", "Context", "build_plan", "KINDS",
           "Client", "Loader", "PROVENANCE_ENTITY", "check_seed", "dump_instance", "__version__"]
