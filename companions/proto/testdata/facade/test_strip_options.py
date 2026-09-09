"""Descriptor-level tests for the strip-options companion.

An enum with `option allow_alias = true` carries two names on one number, which
is only legal while the flag is set. The strip step must drop custom/extension
options without discarding allow_alias, or the generator's reparse rejects the
descriptor (issue #398).
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest
from google.protobuf.descriptor_pb2 import (
    FieldDescriptorProto,
    FileDescriptorProto,
    FileDescriptorSet,
)

import strip_custom_options as strip_options


def _aliased_enum(container, name):
    enum = container.enum_type.add(name=name)
    enum.options.allow_alias = True
    enum.options.deprecated = True
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
    assert enum.options.deprecated is False  # non-alias options still stripped


def test_strip_preserves_allow_alias_on_nested_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    message = fd.message_type.add(name="Subject")
    _aliased_enum(message, "Kind")

    strip_options.strip(fd, {"common.proto"})

    enum = fd.message_type[0].enum_type[0]
    assert enum.options.allow_alias is True
    assert enum.options.deprecated is False


def test_strip_clears_options_on_non_aliased_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    enum = fd.enum_type.add(name="Plain")
    enum.options.deprecated = True
    enum.value.add(name="PLAIN_UNSPECIFIED", number=0)

    strip_options.strip(fd, {"common.proto"})

    assert not fd.enum_type[0].HasField("options")


def test_strip_clears_oneof_options():
    # A oneof carrying a custom option (e.g. buf.validate.oneof, ext 1159) whose
    # defining file is dropped from the output would fail reparse with
    # "OneofOptions: unable to resolve extension"; the option must be stripped.
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    message = fd.message_type.add(name="Subject")
    oneof = message.oneof_decl.add(name="ref")
    oneof.options.uninterpreted_option.add()

    strip_options.strip(fd, {"common.proto"})

    assert not fd.message_type[0].oneof_decl[0].HasField("options")


def test_strip_clears_enum_value_options():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    enum = _aliased_enum(fd, "SubjectKind")
    enum.value[2].options.deprecated = True

    strip_options.strip(fd, {"common.proto"})

    stripped = fd.enum_type[0]
    assert stripped.options.allow_alias is True
    assert not stripped.value[2].HasField("options")


def test_strip_clears_extension_options():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    file_ext = fd.extension.add(
        name="file_ext", number=1000, extendee=".google.protobuf.MessageOptions"
    )
    file_ext.options.deprecated = True
    message = fd.message_type.add(name="Subject")
    msg_ext = message.extension.add(
        name="msg_ext", number=1001, extendee=".google.protobuf.MessageOptions"
    )
    msg_ext.options.deprecated = True

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
    assert enum.options.allow_alias is True
    assert enum.options.deprecated is False


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
    )

    assert "buf/validate/validate.proto" not in out
    api_keys = out["saas/accounts/v1/api_keys.proto"]
    assert "saas/accounts/v1/common.proto" in api_keys.dependency
    assert "buf/validate/validate.proto" not in api_keys.dependency


def test_main_keeps_transitively_referenced_package(tmp_path, monkeypatch):
    # saas/jobs/v1/jobs.proto is not a target, but an rpc resolves into it, so
    # its bindings must be generated too — otherwise the facade has no module
    # for the response type.
    out = _run(
        tmp_path, monkeypatch, _accounts_source(),
        "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
    )

    assert "saas/jobs/v1/jobs.proto" in out
    assert "saas/jobs/v1/jobs.proto" in out["saas/accounts/v1/api_keys.proto"].dependency


def test_main_keeps_topological_order(tmp_path, monkeypatch):
    out = _run(
        tmp_path, monkeypatch, _accounts_source(),
        "saas/accounts/v1/common.proto", "saas/accounts/v1/api_keys.proto",
    )

    names = list(out)
    assert names.index("saas/jobs/v1/jobs.proto") < names.index("saas/accounts/v1/api_keys.proto")
    assert names.index("saas/accounts/v1/common.proto") < names.index("saas/accounts/v1/api_keys.proto")


def test_main_drops_option_only_file_referenced_by_no_field(tmp_path, monkeypatch):
    # An org-shared options proto carries messages too; nothing references them
    # through a field or rpc, so the closure must leave it out.
    source = _accounts_source()
    policy = source.file.add(name="saas/policy/v1/options.proto", package="saas.policy.v1")
    policy.message_type.add(name="PolicyRule")
    api_keys = next(f for f in source.file if f.name == "saas/accounts/v1/api_keys.proto")
    api_keys.dependency.append("saas/policy/v1/options.proto")

    out = _run(tmp_path, monkeypatch, source, "saas/accounts/v1/api_keys.proto")

    assert "saas/policy/v1/options.proto" not in out


@pytest.mark.skipif(shutil.which("protoc") is None, reason="protoc not installed")
def test_stripped_descriptor_regenerates_with_protoc(tmp_path, monkeypatch):
    """The end-to-end shape of issue #410: a module whose files import each
    other and reach into another package. protoc reparses the stripped
    descriptor, so a missing edge fails here exactly as it does in the
    companion."""
    fixtures = Path(__file__).parent
    targets = ["saas/accounts/v1/api_keys.proto", "saas/accounts/v1/common.proto"]
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
