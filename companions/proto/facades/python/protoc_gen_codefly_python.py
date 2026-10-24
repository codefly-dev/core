"""protoc plugin `protoc-gen-codefly-python`.

The companion's Python bindings: for every file buf asks it to generate, the
`*_pb2` messages and the `*_pb2_grpc` stubs, compiled by the `grpc_tools`
pinned in the companion's venv. buf hands a plugin descriptors, not sources, so
the request's files are given to that compiler as a descriptor set
(`--descriptor_set_in`), and what it writes is answered back to buf.

It replaces buf's remote `protocolbuffers/python` and `grpc/python` plugins,
whose versions moved on their own: their gencode outran the protobuf runtime a
generated library declares, and every import of its bindings failed. Here the
gencode is the companion image's, versioned with it, as Go's local plugins are.
"""

from __future__ import annotations

import os
import sys
import tempfile

from google.protobuf.compiler import plugin_pb2
from google.protobuf.descriptor_pb2 import FileDescriptorSet
from grpc_tools import protoc


def generate(request: plugin_pb2.CodeGeneratorRequest) -> plugin_pb2.CodeGeneratorResponse:
    response = plugin_pb2.CodeGeneratorResponse()
    response.supported_features = plugin_pb2.CodeGeneratorResponse.FEATURE_PROTO3_OPTIONAL
    with tempfile.TemporaryDirectory() as work:
        descriptors = os.path.join(work, "request.binpb")
        with open(descriptors, "wb") as out:
            out.write(FileDescriptorSet(file=request.proto_file).SerializeToString())
        output = os.path.join(work, "out")
        os.mkdir(output)
        code = protoc.main([
            "protoc",
            f"--descriptor_set_in={descriptors}",
            f"--python_out={output}",
            f"--grpc_python_out={output}",
            *request.file_to_generate,
        ])
        if code != 0:
            response.error = f"grpc_tools.protoc exited {code} generating {', '.join(request.file_to_generate)}"
            return response
        for root, _, files in os.walk(output):
            for name in sorted(files):
                path = os.path.join(root, name)
                generated = response.file.add()
                generated.name = os.path.relpath(path, output).replace(os.sep, "/")
                with open(path, encoding="utf-8") as source:
                    generated.content = source.read()
    return response


def main() -> None:
    request = plugin_pb2.CodeGeneratorRequest.FromString(sys.stdin.buffer.read())
    sys.stdout.buffer.write(generate(request).SerializeToString())


if __name__ == "__main__":
    main()
