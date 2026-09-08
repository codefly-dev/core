"""Descriptor-level tests for the strip-options companion.

An enum with `option allow_alias = true` carries two names on one number, which
is only legal while the flag is set. The strip step must drop custom/extension
options without discarding allow_alias, or the generator's reparse rejects the
descriptor (issue #398).
"""

from __future__ import annotations

from google.protobuf.descriptor_pb2 import FileDescriptorProto, FileDescriptorSet

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

    strip_options.strip(fd)

    enum = fd.enum_type[0]
    assert enum.options.allow_alias is True
    assert enum.options.deprecated is False  # non-alias options still stripped


def test_strip_preserves_allow_alias_on_nested_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    message = fd.message_type.add(name="Subject")
    _aliased_enum(message, "Kind")

    strip_options.strip(fd)

    enum = fd.message_type[0].enum_type[0]
    assert enum.options.allow_alias is True
    assert enum.options.deprecated is False


def test_strip_clears_options_on_non_aliased_enum():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    enum = fd.enum_type.add(name="Plain")
    enum.options.deprecated = True
    enum.value.add(name="PLAIN_UNSPECIFIED", number=0)

    strip_options.strip(fd)

    assert not fd.enum_type[0].HasField("options")


def test_strip_clears_oneof_options():
    # A oneof carrying a custom option (e.g. buf.validate.oneof, ext 1159) whose
    # defining file is dropped from the output would fail reparse with
    # "OneofOptions: unable to resolve extension"; the option must be stripped.
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    message = fd.message_type.add(name="Subject")
    oneof = message.oneof_decl.add(name="ref")
    oneof.options.uninterpreted_option.add()

    strip_options.strip(fd)

    assert not fd.message_type[0].oneof_decl[0].HasField("options")


def test_strip_clears_enum_value_options():
    fd = FileDescriptorProto(name="common.proto", package="svc.v1")
    enum = _aliased_enum(fd, "SubjectKind")
    enum.value[2].options.deprecated = True

    strip_options.strip(fd)

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

    strip_options.strip(fd)

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
