import os, sys
sys.path.insert(0, os.path.abspath(os.path.dirname(__file__)))

import subprocess
import time
import grpc
from concurrent import futures
from google.protobuf.empty_pb2 import Empty

import cli.v0.cli_pb2_grpc as cli_grpc

class Launcher:
    def __init__(self):
        self.cmd = None
        self.cli = None

    def launch_up_to(self):
        self.cmd = subprocess.Popen(["codefly", "run", "service", "--exclude-root", "--cli-server", "--random-ports"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        port = 10000
        wait = 5
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

    def close(self):
        self.cli.StopFlow(Empty())