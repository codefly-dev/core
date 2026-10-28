import importlib.util
from pathlib import Path

import debugpy

assert importlib.util.find_spec("pip") is None
assert importlib.util.find_spec("ensurepip") is None
Path(__file__).with_name("result.txt").write_text("debugpy without pip")
