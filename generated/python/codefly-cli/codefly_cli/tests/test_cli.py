import sys
import os

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..')))

from codefly_cli.ci import Launcher
from codefly_cli.ci import with_dependencies, with_cli_logs, with_debug, with_code_path, with_silent

def test_nothing():
    assert True