"""Descriptor-level tests for the strip-options companion.

The strip drops custom options (extensions of a google.protobuf.*Options
message, whose defining files leave the output) and keeps the built-in ones,
which change what the descriptor means. An enum with `option allow_alias = true`
carries two names on one number, which is only legal while the flag is set, or
the generator's reparse rejects the descriptor (issue #398). A `map<K, V>` field
is a repeated entry message marked `map_entry`; without the flag the generated
class has a repeated field where the proto declares a map.
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path

import pytest
from google.protobuf import descriptor_pool, message_factory
from google.protobuf.descriptor_pb2 import (
    FieldDescriptorProto,
    FileDescriptorProto,
    FileDescriptorSet,
)

import strip_custom_options as strip_options


# buf.validate's field option number: an extension the strip's protobuf runtime
# has never heard of, so it parses as an unknown field — exactly how a real
# custom option arrives in the image.
_CUSTOM_OPTION = 1159


def _set_custom_option(options, number=_CUSTOM_OPTION):
    """Add an unregistered varint extension (number = 1) to an options message."""
    tag, encoded = (number << 3) | 0, b""
    while tag > 0x7F:
        encoded += bytes([(tag & 0x7F) | 0x80])
        tag >>= 7
    options.MergeFromString(encoded + bytes([tag, 1]))
    assert options.ByteSize() > 0


def _aliased_enum(container, name):
    enum = container.enum_type.add(name=name)
    enum.options.allow_alias = True
    _set_custom_option(enum.options)
    enum.value.add(name=f"{name}_UNSPECIFIED", number=0)
    enum.value.add(name=f"{name}_PRINCIPAL", number=1)
    enum.value.add(name=f"{name}_USER", number=1)
    return enum


def test_strip_preserves_allow_alias_on_top_level_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    _aliased_enum(fd, "SubjectKind")

    strip_options.strip(fd, {"common.proto"})

    enum = fd.enum_type[0]
    assert enum.options.allow_alias is True
    assert enum.options == type(enum.options)(allow_alias=True)  # custom stripped


def test_strip_preserves_allow_alias_on_nested_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    message = fd.message_type.add(name="Subject")
    _aliased_enum(message, "Kind")

    strip_options.strip(fd, {"common.proto"})

    enum = fd.message_type[0].enum_type[0]
    assert enum.options.allow_alias is True
    assert enum.options == type(enum.options)(allow_alias=True)


def test_strip_clears_custom_only_options_on_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    enum = fd.enum_type.add(name="Plain")
    _set_custom_option(enum.options)
    enum.value.add(name="PLAIN_UNSPECIFIED", number=0)

    strip_options.strip(fd, {"common.proto"})

    assert not fd.enum_type[0].HasField("options")


def test_strip_keeps_built_in_options_everywhere():
    # Built-in options are declared fields of the options messages, not
    # extensions: they belong to the descriptor's meaning, never to a dropped
    # file, so none of them is stripped alongside the custom ones.
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    fd.options.go_package = "example.com/svc/v1"
    _set_custom_option(fd.options)
    message = fd.message_type.add(name="Subject")
    message.options.deprecated = True
    field = message.field.add(
        name="ids", number=1, type=FieldDescriptorProto.TYPE_INT32,
        label=FieldDescriptorProto.LABEL_REPEATED,
    )
    field.options.packed = True
    _set_custom_option(field.options)
    service = fd.service.add(name="Svc")
    method = service.method.add(name="Get", input_type=".svc.v1.Subject", output_type=".svc.v1.Subject")
    method.options.idempotency_level = method.options.NO_SIDE_EFFECTS
    _set_custom_option(method.options, number=72295728)  # google.api.http

    strip_options.strip(fd, {"common.proto"})

    assert fd.options == type(fd.options)(go_package="example.com/svc/v1")
    assert fd.message_type[0].options.deprecated is True
    assert fd.message_type[0].field[0].options == type(field.options)(packed=True)
    kept = fd.service[0].method[0].options
    assert kept == type(kept)(idempotency_level=kept.NO_SIDE_EFFECTS)


def test_strip_clears_oneof_options():
    # A oneof carrying a custom option (e.g. buf.validate.oneof, ext 1159) whose
    # defining file is dropped from the output would fail reparse with
    # "OneofOptions: unable to resolve extension"; the option must be stripped.
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    message = fd.message_type.add(name="Subject")
    oneof = message.oneof_decl.add(name="ref")
    oneof.options.uninterpreted_option.add()
    _set_custom_option(oneof.options)

    strip_options.strip(fd, {"common.proto"})

    assert not fd.message_type[0].oneof_decl[0].HasField("options")


def test_strip_clears_enum_value_options():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    enum = _aliased_enum(fd, "SubjectKind")
    _set_custom_option(enum.value[2].options)

    strip_options.strip(fd, {"common.proto"})

    stripped = fd.enum_type[0]
    assert stripped.options.allow_alias is True
    assert not stripped.value[2].HasField("options")


def test_strip_clears_extension_options():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    file_ext = fd.extension.add(
        name="file_ext", number=1000, extendee=".google.protobuf.MessageOptions"
    )
    _set_custom_option(file_ext.options)
    message = fd.message_type.add(name="Subject")
    msg_ext = message.extension.add(
        name="msg_ext", number=1001, extendee=".google.protobuf.MessageOptions"
    )
    _set_custom_option(msg_ext.options)

    strip_options.strip(fd, {"common.proto"})

    assert not fd.extension[0].HasField("options")
    assert not fd.message_type[0].extension[0].HasField("options")


def test_main_round_trips_allow_alias(tmp_path, monkeypatch):
    source = FileDescriptorSet()
    fd = source.file.add(name="common.proto", package="svc.v1")
    _aliased_enum(fd, "SubjectKind")

    in_path = tmp_path / "in.binpb"
    out_path = tmp_path / "out.binpb"
    in_path.write_bytes(source.SerializeToString())

    monkeypatch.setattr(
        strip_options.sys, "argv",
        ["strip_custom_options", str(in_path), str(out_path), "common.proto"],
    )
    strip_options.main()

    out = FileDescriptorSet.FromString(out_path.read_bytes())
    enum = out.file[0].enum_type[0]
    assert enum.options == type(enum.options)(allow_alias=True)


def _run(tmp_path, monkeypatch, source, *targets):
    in_path = tmp_path / "in.binpb"
    out_path = tmp_path / "out.binpb"
    in_path.write_bytes(source.SerializeToString())
    monkeypatch.setattr(
        strip_options.sys, "argv",
        ["strip_custom_options", str(in_path), str(out_path), *targets],
    )
    strip_options.main()
    out = FileDescriptorSet.FromString(out_path.read_bytes())
    return {f.name: f for f in out.file}


def _accounts_source():
    """buf.validate (options only), a jobs contract in another package, and two
    accounts files where api_keys.proto references both."""
    source = FileDescriptorSet()
    source.file.add(name="buf/validate/validate.proto", package="buf.validate")

    jobs = source.file.add(name="saas/jobs/v1/jobs.proto", package="saas.jobs.v1")
    jobs.message_type.add(name="GetJobOperationsResponse")

    common = source.file.add(name="saas/accounts/v1/common.proto", package="saas.accounts.v1")
    common.dependency.append("buf/validate/validate.proto")
    common.enum_type.add(name="Permission").value.add(name="PERMISSION_UNSPECIFIED", number=0)

    api_keys = source.file.add(name="saas/accounts/v1/api_keys.proto", package="saas.accounts.v1")
    api_keys.dependency.extend(
        [
            "buf/validate/validate.proto",
            "saas/accounts/v1/common.proto",
            "saas/jobs/v1/jobs.proto",
        ]
    )
    key = api_keys.message_type.add(name="APIKey")
    key.field.add(
        name="scopes",
        number=1,
        type_name=".saas.accounts.v1.Permission",
        label=FieldDescriptorProto.LABEL_REPEATED,
        type=FieldDescriptorProto.TYPE_ENUM,
    )
    api_keys.message_type.add(name="ListJobsRequest")
    service = api_keys.service.add(name="APIKeyService")
    service.method.add(
        name="ListJobs",
        input_type=".saas.accounts.v1.ListJobsRequest",
        output_type=".saas.jobs.v1.GetJobOperationsResponse",
    )
    return source


def test_main_keeps_import_edge_between_targets(tmp_path, monkeypatch):
    # Both files survive the strip, so dropping the edge between them leaves
    # APIKey.scopes referring to a type protoc considers un-imported (issue #410).
    out = _run(
        tmp_path, monkeypatch, _accounts_source(),
        "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
        "saas/jobs/v1/jobs.proto",
    )

    assert "buf/validate/validate.proto" not in out
    api_keys = out["saas/accounts/v1/api_keys.proto"]
    assert "saas/accounts/v1/common.proto" in api_keys.dependency
    assert "buf/validate/validate.proto" not in api_keys.dependency


def test_main_keeps_declared_cross_package_file(tmp_path, monkeypatch):
    out = _run(
        tmp_path, monkeypatch, _accounts_source(),
        "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
        "saas/jobs/v1/jobs.proto",
    )

    assert "saas/jobs/v1/jobs.proto" in out
    assert "saas/jobs/v1/jobs.proto" in out["saas/accounts/v1/api_keys.proto"].dependency


def test_main_refuses_undeclared_referenced_file(tmp_path, monkeypatch):
    # saas/jobs/v1/jobs.proto holds an rpc response type. Pulling it in silently
    # would put another contract's descriptors in this library, so the strip
    # names it and stops instead.
    with pytest.raises(SystemExit) as caught:
        _run(
            tmp_path, monkeypatch, _accounts_source(),
            "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
        )

    message = str(caught.value)
    assert "saas/jobs/v1/jobs.proto" in message
    assert "not declared as targets" in message


def test_main_refuses_undeclared_sibling_file(tmp_path, monkeypatch):
    with pytest.raises(SystemExit) as caught:
        _run(
            tmp_path, monkeypatch, _accounts_source(),
            "saas/accounts/v1/api_keys.proto", "saas/jobs/v1/jobs.proto",
        )

    assert "saas/accounts/v1/common.proto" in str(caught.value)


def test_main_refuses_googleapis_common_protos(tmp_path, monkeypatch):
    # google/rpc/code.proto is shipped by googleapis-common-protos, which
    # registers that exact file name at import; a vendored copy collides in the
    # descriptor pool. google/protobuf/* is the exception — it is the runtime.
    source = _accounts_source()
    code = source.file.add(name="google/rpc/code.proto", package="google.rpc")
    code.enum_type.add(name="Code").value.add(name="OK", number=0)
    api_keys = next(f for f in source.file if f.name == "saas/accounts/v1/api_keys.proto")
    api_keys.dependency.append("google/rpc/code.proto")
    api_keys.message_type[0].field.add(
        name="status",
        number=3,
        type_name=".google.rpc.Code",
        label=FieldDescriptorProto.LABEL_OPTIONAL,
        type=FieldDescriptorProto.TYPE_ENUM,
    )

    with pytest.raises(SystemExit) as caught:
        _run(
            tmp_path, monkeypatch, source,
            "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
            "saas/jobs/v1/jobs.proto",
        )

    message = str(caught.value)
    assert "google/rpc/code.proto" in message
    assert "googleapis-common-protos" in message


def test_main_refuses_extension_reference_to_undeclared_file(tmp_path, monkeypatch):
    # An extension's type is as load-bearing as a field's: dropping the file it
    # resolves to leaves the same unbuildable descriptor.
    source = _accounts_source()
    shared = source.file.add(name="saas/shared/v1/meta.proto", package="saas.shared.v1")
    shared.message_type.add(name="Meta")
    api_keys = next(f for f in source.file if f.name == "saas/accounts/v1/api_keys.proto")
    api_keys.extension.add(
        name="meta",
        number=1000,
        extendee=".google.protobuf.MessageOptions",
        type_name=".saas.shared.v1.Meta",
        label=FieldDescriptorProto.LABEL_OPTIONAL,
        type=FieldDescriptorProto.TYPE_MESSAGE,
    )

    with pytest.raises(SystemExit) as caught:
        _run(
            tmp_path, monkeypatch, source,
            "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
            "saas/jobs/v1/jobs.proto",
        )

    assert "saas/shared/v1/meta.proto" in str(caught.value)


def test_main_keeps_topological_order(tmp_path, monkeypatch):
    out = _run(
        tmp_path, monkeypatch, _accounts_source(),
        "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
        "saas/jobs/v1/jobs.proto",
    )

    names = list(out)
    assert names.index("saas/jobs/v1/jobs.proto") < names.index("saas/accounts/v1/api_keys.proto")
    assert names.index("saas/accounts/v1/common.proto") < names.index("saas/accounts/v1/api_keys.proto")


def test_main_drops_option_only_file_referenced_by_no_field(tmp_path, monkeypatch):
    # An org-shared options proto carries messages too; nothing references them
    # through a field, an rpc, or an extension, so it stays out of the output
    # and out of the declared-target check.
    source = _accounts_source()
    policy = source.file.add(name="saas/policy/v1/options.proto", package="saas.policy.v1")
    policy.message_type.add(name="PolicyRule")
    api_keys = next(f for f in source.file if f.name == "saas/accounts/v1/api_keys.proto")
    api_keys.dependency.append("saas/policy/v1/options.proto")

    out = _run(
        tmp_path, monkeypatch, source,
        "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
        "saas/jobs/v1/jobs.proto",
    )

    assert "saas/policy/v1/options.proto" not in out


def test_main_carries_transitive_well_known_types(tmp_path, monkeypatch):
    # google/protobuf/api.proto imports source_context.proto and type.proto.
    # Emitting it without them leaves a descriptor protoc cannot build.
    source = FileDescriptorSet()
    source.file.add(name="google/protobuf/source_context.proto", package="google.protobuf")
    source.file.add(name="google/protobuf/type.proto", package="google.protobuf")
    api = source.file.add(name="google/protobuf/api.proto", package="google.protobuf")
    api.dependency.extend(
        ["google/protobuf/source_context.proto", "google/protobuf/type.proto"]
    )
    api.message_type.add(name="Api")
    own = source.file.add(name="svc/v1/thing.proto", package="svc.v1")
    own.dependency.append("google/protobuf/api.proto")
    thing = own.message_type.add(name="Thing")
    thing.field.add(
        name="api",
        number=1,
        type_name=".google.protobuf.Api",
        label=FieldDescriptorProto.LABEL_OPTIONAL,
        type=FieldDescriptorProto.TYPE_MESSAGE,
    )

    out = _run(tmp_path, monkeypatch, source, "svc/v1/thing.proto")

    assert "google/protobuf/source_context.proto" in out
    assert "google/protobuf/type.proto" in out


@pytest.mark.skipif(shutil.which("protoc") is None, reason="protoc not installed")
def test_stripped_descriptor_regenerates_with_protoc(tmp_path, monkeypatch):
    """The end-to-end shape of issue #410: a module whose files import each
    other and reach into another package. protoc reparses the stripped
    descriptor, so a missing edge fails here exactly as it does in the
    companion."""
    fixtures = Path(__file__).parent
    targets = [
        "saas/accounts/v1/api_keys.proto",
        "saas/accounts/v1/common.proto",
        "saas/jobs/v1/jobs.proto",
    ]
    descriptors = tmp_path / "in.binpb"
    subprocess.run(
        ["protoc", "-I", str(fixtures), "--include_imports",
         f"--descriptor_set_out={descriptors}", *targets],
        check=True,
    )

    stripped = tmp_path / "out.binpb"
    monkeypatch.setattr(
        strip_options.sys, "argv",
        ["strip_custom_options", str(descriptors), str(stripped), *targets],
    )
    strip_options.main()

    out = FileDescriptorSet.FromString(stripped.read_bytes())
    generated = tmp_path / "gen"
    generated.mkdir()
    subprocess.run(
        ["protoc", f"--descriptor_set_in={stripped}", f"--python_out={generated}",
         *[f.name for f in out.file]],
        check=True,
    )

    assert (generated / "saas/jobs/v1/jobs_pb2.py").exists()


def _map_source():
    """svc/v1/grid.proto as protoc compiles `map<string, string> labels = 1;
    map<string, Cell> cells = 2;` — each map is a repeated field of a nested
    entry message marked map_entry — with a custom option on the map field."""
    source = FileDescriptorSet()
    grid = source.file.add(name="svc/v1/grid.proto", package="svc.v1", syntax="proto3")
    grid.message_type.add(name="Cell").field.add(
        name="value", number=1, json_name="value",
        type=FieldDescriptorProto.TYPE_STRING, label=FieldDescriptorProto.LABEL_OPTIONAL,
    )
    sheet = grid.message_type.add(name="Sheet")
    for number, (name, entry, value_type, value_type_name) in enumerate(
        [
            ("labels", "LabelsEntry", FieldDescriptorProto.TYPE_STRING, ""),
            ("cells", "CellsEntry", FieldDescriptorProto.TYPE_MESSAGE, ".svc.v1.Cell"),
        ],
        start=1,
    ):
        nested = sheet.nested_type.add(name=entry)
        nested.options.map_entry = True
        nested.field.add(
            name="key", number=1, json_name="key",
            type=FieldDescriptorProto.TYPE_STRING, label=FieldDescriptorProto.LABEL_OPTIONAL,
        )
        value = nested.field.add(
            name="value", number=2, json_name="value", type=value_type,
            label=FieldDescriptorProto.LABEL_OPTIONAL,
        )
        if value_type_name:
            value.type_name = value_type_name
        field = sheet.field.add(
            name=name, number=number, json_name=name,
            type=FieldDescriptorProto.TYPE_MESSAGE, label=FieldDescriptorProto.LABEL_REPEATED,
            type_name=f".svc.v1.Sheet.{entry}",
        )
        _set_custom_option(field.options)
    return source


def test_main_keeps_map_entry_so_a_map_field_stays_a_map(tmp_path, monkeypatch):
    # The regression: clearing every options message dropped map_entry, so the
    # generated class exposed `cells` as a repeated CellsEntry a caller had to
    # .add() to, where the proto declares a map.
    out = _run(tmp_path, monkeypatch, _map_source(), "svc/v1/grid.proto")

    sheet = out["svc/v1/grid.proto"].message_type[1]
    for entry in sheet.nested_type:
        assert entry.options.map_entry is True
    for field in sheet.field:
        assert not field.HasField("options")  # the custom option is gone

    # Build the class from the stripped descriptor, as the generated _pb2 does.
    pool = descriptor_pool.DescriptorPool()
    pool.Add(out["svc/v1/grid.proto"])
    Sheet = message_factory.GetMessageClass(pool.FindMessageTypeByName("svc.v1.Sheet"))
    message = Sheet()
    message.labels["region"] = "eu"
    message.cells["A1"].value = "42"
    assert dict(message.labels) == {"region": "eu"}
    assert message.cells["A1"].value == "42"
    assert Sheet.FromString(message.SerializeToString()) == message


@pytest.mark.skipif(shutil.which("protoc") is None, reason="protoc not installed")
def test_stripped_map_field_generates_a_python_map(tmp_path, monkeypatch):
    """End to end through protoc --python_out: the generated module's map
    fields are maps, not repeated entry messages."""
    out = _run(tmp_path, monkeypatch, _map_source(), "svc/v1/grid.proto")
    stripped = tmp_path / "stripped.binpb"
    stripped.write_bytes(
        FileDescriptorSet(file=list(out.values())).SerializeToString()
    )
    generated = tmp_path / "gen"
    generated.mkdir()
    subprocess.run(
        ["protoc", f"--descriptor_set_in={stripped}", f"--python_out={generated}",
         "svc/v1/grid.proto"],
        check=True,
    )

    script = (
        "from svc.v1 import grid_pb2\n"
        "sheet = grid_pb2.Sheet()\n"
        "sheet.labels['region'] = 'eu'\n"
        "sheet.cells['A1'].value = '42'\n"
        "assert dict(sheet.labels) == {'region': 'eu'}, sheet\n"
        "assert sheet.cells['A1'].value == '42', sheet\n"
    )
    # A fresh interpreter: the generated module registers into the default
    # pool, which this process shares with every other test.
    subprocess.run([sys.executable, "-c", script], cwd=generated, check=True)
