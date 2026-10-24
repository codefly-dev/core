"""Tests for the companion's Python plugin, `protoc-gen-codefly-python`.

A generated client must import only what its own types need. An import used
only for an option (buf/validate, google/api) became an absolute import no PyPI
distribution supplies, so every `import` of the bindings failed.
"""

from __future__ import annotations

import importlib.util
import sys

import pytest
from google.protobuf.compiler import plugin_pb2
from google.protobuf.descriptor_pb2 import (
    DescriptorProto,
    FieldDescriptorProto,
    FileDescriptorProto,
)

import protoc_gen_codefly_python as plugin

_STRING = FieldDescriptorProto.TYPE_STRING
_MESSAGE = FieldDescriptorProto.TYPE_MESSAGE
_OPTIONAL = FieldDescriptorProto.LABEL_OPTIONAL


def _descriptor_proto() -> FileDescriptorProto:
    from google.protobuf import descriptor_pb2

    fd = FileDescriptorProto()
    descriptor_pb2.DESCRIPTOR.CopyToProto(fd)
    return fd


def _validate() -> FileDescriptorProto:
    """A stand-in for buf/validate/validate.proto: an extension of FieldOptions."""
    fd = FileDescriptorProto(name="buf/validate/validate.proto", package="buf.validate", syntax="proto3")
    fd.dependency.append("google/protobuf/descriptor.proto")
    fd.message_type.add(name="FieldRules").field.add(name="required", number=1, type=FieldDescriptorProto.TYPE_BOOL, label=_OPTIONAL)
    fd.extension.add(
        name="field", number=1159, label=_OPTIONAL, type=_MESSAGE,
        type_name=".buf.validate.FieldRules", extendee=".google.protobuf.FieldOptions",
    )
    return fd


def _status() -> FileDescriptorProto:
    fd = FileDescriptorProto(name="google/rpc/status.proto", package="google.rpc", syntax="proto3")
    fd.message_type.add(name="Status").field.add(name="code", number=1, type=FieldDescriptorProto.TYPE_INT32, label=_OPTIONAL)
    return fd


def _demo() -> FileDescriptorProto:
    """Imports validate for a field option only, and google/rpc for a field type."""
    fd = FileDescriptorProto(name="demo/v1/demo.proto", package="demo.v1", syntax="proto3")
    fd.dependency.extend(["buf/validate/validate.proto", "google/rpc/status.proto"])
    fd.options.go_package = "example.com/demo/v1;demov1"
    ping = fd.message_type.add(name="Ping")
    field = ping.field.add(name="id", number=1, type=_STRING, label=_OPTIONAL)
    field.options.deprecated = True
    # buf.validate.field, as an unknown extension field on FieldOptions: what a
    # descriptor parsed without the extension registered carries.
    field.options.MergeFromString(b"\xba\x48\x02\x08\x01")
    ping.field.add(name="status", number=2, type=_MESSAGE, type_name=".google.rpc.Status", label=_OPTIONAL)
    method = fd.service.add(name="Demo").method.add(name="Echo", input_type=".demo.v1.Ping", output_type=".demo.v1.Ping")
    method.options.idempotency_level = method.options.NO_SIDE_EFFECTS
    return fd


def _files():
    return [_descriptor_proto(), _validate(), _status(), _demo()]


def test_an_import_used_only_for_an_option_is_dropped():
    demo = {f.name: f for f in plugin.without_options(_files())}["demo/v1/demo.proto"]
    assert list(demo.dependency) == ["google/rpc/status.proto"]


def test_options_are_cleared_and_the_input_left_untouched():
    files = _files()
    demo = {f.name: f for f in plugin.without_options(files)}["demo/v1/demo.proto"]
    assert not demo.HasField("options")
    assert not demo.message_type[0].field[0].HasField("options")
    assert not demo.service[0].method[0].HasField("options")
    # The request's own descriptors are buf's; the plugin works on copies.
    assert "buf/validate/validate.proto" in files[3].dependency
    assert files[3].message_type[0].field[0].options.deprecated


def test_the_well_known_types_are_kept():
    validate = {f.name: f for f in plugin.without_options(_files())}["buf/validate/validate.proto"]
    assert list(validate.dependency) == ["google/protobuf/descriptor.proto"]


def test_an_import_public_chain_that_supplies_a_type_is_kept():
    types = FileDescriptorProto(name="lib/types.proto", package="lib", syntax="proto3")
    types.message_type.add(name="Thing")
    reexport = FileDescriptorProto(name="lib/all.proto", package="lib", syntax="proto3")
    reexport.dependency.extend(["lib/types.proto", "buf/validate/validate.proto"])
    reexport.public_dependency.append(0)
    user = FileDescriptorProto(name="app/app.proto", package="app", syntax="proto3")
    user.dependency.append("lib/all.proto")
    user.message_type.add(name="Holder").field.add(
        name="thing", number=1, type=_MESSAGE, type_name=".lib.Thing", label=_OPTIONAL,
    )

    out = {f.name: f for f in plugin.without_options([_descriptor_proto(), _validate(), types, reexport, user])}
    assert list(out["app/app.proto"].dependency) == ["lib/all.proto"]
    assert list(out["lib/all.proto"].dependency) == ["lib/types.proto"]
    assert list(out["lib/all.proto"].public_dependency) == [0]


def test_generated_bindings_import_without_buf_validate(tmp_path, monkeypatch):
    pytest.importorskip("grpc_tools")
    pytest.importorskip("google.rpc.status_pb2")
    request = plugin_pb2.CodeGeneratorRequest(file_to_generate=["demo/v1/demo.proto"])
    request.proto_file.extend(_files())

    response = plugin.generate(request)
    assert not response.error, response.error
    generated = {f.name: f.content for f in response.file}
    assert set(generated) == {"demo/v1/demo_pb2.py", "demo/v1/demo_pb2_grpc.py"}
    assert "buf.validate" not in generated["demo/v1/demo_pb2.py"]
    assert "buf/validate" not in generated["demo/v1/demo_pb2.py"]
    assert "from google.rpc import status_pb2" in generated["demo/v1/demo_pb2.py"]

    for name, content in generated.items():
        path = tmp_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
    monkeypatch.syspath_prepend(str(tmp_path))
    spec = importlib.util.spec_from_file_location("demo.v1.demo_pb2", tmp_path / "demo/v1/demo_pb2.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    assert module.Ping(id="x").id == "x"
