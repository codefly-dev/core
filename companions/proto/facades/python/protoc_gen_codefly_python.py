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

Every file is compiled without its options, and keeps only the imports its own
types need. An import used only for an option — `buf/validate/validate.proto`,
`google/api/annotations.proto`, a module's own policy options — would otherwise
become an absolute `from buf.validate import validate_pb2` in the bindings,
which no PyPI distribution supplies, and register a shared descriptor into the
global pool. A client reads none of those options; what it sends is checked by
the service that declares them.
"""

from __future__ import annotations

import os
import sys
import tempfile

from google.protobuf.compiler import plugin_pb2
from google.protobuf.descriptor_pb2 import FileDescriptorSet
from grpc_tools import protoc

from strip_custom_options import clear_enum, clear_message, referenced_types, type_owners

_WELL_KNOWN_PREFIX = "google/protobuf/"


def without_options(files):
    """files, each with its options cleared and its dependencies narrowed to the
    ones that define a type it references (directly, or through an `import
    public` chain), the well-known types, which ship with the runtime, and its
    own `import public` re-exports, which are part of what it offers."""
    by_name = {f.name: f for f in files}
    owners = type_owners(files)
    stripped = []
    for original in files:
        file_proto = type(original)()
        file_proto.CopyFrom(original)
        owning = {owners[ref] for ref in referenced_types(file_proto) if ref in owners}
        reexported = {file_proto.dependency[i] for i in file_proto.public_dependency}
        kept = [
            dep
            for dep in file_proto.dependency
            if dep.startswith(_WELL_KNOWN_PREFIX) or dep in reexported or owning & _public_closure(by_name, dep)
        ]
        public = [kept.index(file_proto.dependency[i]) for i in file_proto.public_dependency
                  if file_proto.dependency[i] in kept]
        del file_proto.dependency[:]
        file_proto.dependency.extend(kept)
        del file_proto.public_dependency[:]
        file_proto.public_dependency.extend(public)
        del file_proto.weak_dependency[:]
        file_proto.ClearField("options")
        for message in file_proto.message_type:
            clear_message(message)
        for enum in file_proto.enum_type:
            clear_enum(enum)
        for extension in file_proto.extension:
            extension.ClearField("options")
        for service in file_proto.service:
            service.ClearField("options")
            for method in service.method:
                method.ClearField("options")
        stripped.append(file_proto)
    return stripped


def _public_closure(by_name, name):
    """name and every file it re-exports through `import public`, transitively:
    the files whose types an import of name makes visible."""
    closure, frontier = set(), [name]
    while frontier:
        current = frontier.pop()
        if current in closure:
            continue
        closure.add(current)
        file_proto = by_name.get(current)
        if file_proto is not None:
            frontier.extend(file_proto.dependency[i] for i in file_proto.public_dependency)
    return closure


def generate(request: plugin_pb2.CodeGeneratorRequest) -> plugin_pb2.CodeGeneratorResponse:
    response = plugin_pb2.CodeGeneratorResponse()
    response.supported_features = plugin_pb2.CodeGeneratorResponse.FEATURE_PROTO3_OPTIONAL
    with tempfile.TemporaryDirectory() as work:
        descriptors = os.path.join(work, "request.binpb")
        with open(descriptors, "wb") as out:
            out.write(FileDescriptorSet(file=without_options(request.proto_file)).SerializeToString())
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
