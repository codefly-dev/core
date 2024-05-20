import os, sys

# Add the codefly_cli path
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '../codefly_cli')))

import codefly_sdk.codefly as codefly

import subprocess
import time
import grpc
from typing import Optional, List

from codefly_sdk.codefly import init

from codefly_sdk.codefly import find_service_dir, unique_to_env_key

import cli.v0.cli_pb2_grpc as cli_grpc
import cli.v0.cli_pb2 as cli
import base.v0.configuration_pb2 as configuration

from google.protobuf.empty_pb2 import Empty

import pytest


def filter_configurations(configurations: List[configuration.Configuration], runtime_context: str) -> List[configuration.Configuration]:
    return [conf for conf in configurations if conf.runtime_context.kind == runtime_context]

@pytest.fixture
def with_dependencies():
    launcher = Launcher()
    launcher.start()
    codefly.init("..")
    yield
    launcher.close()


def configuration_key(conf: configuration.Configuration, info: configuration.ConfigurationInformation, value: configuration.ConfigurationValue):
    secret_prefix = ""
    if value.secret:
        secret_prefix = "SECRET_"
    if conf.origin == "workspace":
        k = f"CODEFLY__WORKSPACE_{secret_prefix}CONFIGURATION"
    else:
        k = f"CODEFLY__SERVICE_{secret_prefix}CONFIGURATION__{unique_to_env_key(conf.origin)}"
    return f"{k}__{info.name}__{value.key}".upper()


def setup_environment_with_configuration(conf: configuration.Configuration):
    for info in conf.configurations:
        for val in info.configuration_values:
            key = configuration_key(conf, info, val)
            os.environ[key] = val.value

_with_cli_logs = False

def with_cli_logs():
    global _with_cli_logs
    _with_cli_logs = True

_debug = False
def with_debug():
    global _debug
    _debug = True



class Launcher:
    def __init__(self, root: str = "..", scope: str = "", keep_alive: bool = False):
        self.cmd = None
        self.cli = None

        global _with_cli_logs
        self.show_cli_output = _with_cli_logs

        self.dir = find_service_dir(os.path.abspath(root))
        print(f"running in {self.dir}")
        self.scope = scope
        self.keep_alive = keep_alive
        self.module = codefly.get_module()
        self.service = codefly.get_service()
        self.unique = codefly.get_unique()
        self.runtime_context = codefly.runtime_context()


    def __enter__(self):
        self.start()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        if self.keep_alive:
            return
        self.destroy()

    def start(self):
        cmd = ["codefly", "run", "service"]
        global _debug
        if _debug:
            cmd.append("-d")
        cmd.extend("--exclude-root", "--cli-server")
        if self.scope:
            cmd.extend(["--scope", self.scope])
        options = {"stdout": subprocess.PIPE}
        if self.show_cli_output:
            options = {}
        self.cmd = subprocess.Popen(cmd, cwd=self.dir, **options)
        port = 10000
        wait = 60
        while True:
            time.sleep(1)
            try:
                channel = grpc.insecure_channel(f'localhost:{port}')
                self.cli = cli_grpc.CLIStub(channel)
                break
            except Exception as e:
                wait -= 0.5
                if wait <= 0:
                    raise Exception("timeout waiting for connection") from e
                time.sleep(0.5)
        self.wait_for_ready()
        self.setup_environment()

    def wait_for_ready(self):
        time.sleep(1)
        wait = 5
        while True:
            try:
                status = self.cli.GetFlowStatus(Empty())
                if status.ready:
                    break
            except Exception as e:
                wait -= 0.5
                if wait <= 0:
                    raise Exception("timeout waiting for flow to be ready") from e
                time.sleep(0.5)

    def setup_environment(self):
        request = cli.GetConfigurationRequest(module=self.module, service=self.service)
        resp = self.cli.GetDependenciesConfigurations(request)
        dependencies_configurations = filter_configurations(resp.configurations, self.runtime_context)
        for conf in dependencies_configurations:
            setup_environment_with_configuration(conf)

    def close(self):
        self.cli.StopFlow(Empty())
        if self.cmd is not None:
            self.cmd.terminate()
            self.cmd.wait()

    def destroy(self):
        if self.cmd:
            self.cmd.terminate()
            self.cmd.wait()

