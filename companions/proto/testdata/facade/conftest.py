"""Put the ported facade plugin on sys.path so test_facade_gen.py can import it.

The plugin ships as a flat module (`protoc_gen_codefly_facade_python`) rather
than the `solution_runtime.sdk.facade_gen` package it was donated from; this
shim is the only difference from the solution-runtime-python test.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[2] / "facades" / "python"))
