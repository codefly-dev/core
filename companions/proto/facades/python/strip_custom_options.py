"""Strip custom options + shared-proto deps from a buf image so the generated
message bindings embed only the named proto(s) themselves (plus the well-known
types they reference, which come from the protobuf runtime). Removes the
buf.validate, google.api.http, and any other option extension whose defining
file is not one of the target files, so the generated `*_pb2` never registers a
shared descriptor into the global pool — the collision saas-sdk-python
documents.

Usage: codefly-proto-strip-options <in.binpb> <out.binpb> <proto>...

Each <proto> is a file kept in the output with its options and non-well-known
dependencies stripped; the google.protobuf well-known types those files still
reference are carried through as-is. Every other file (buf/validate/*,
google/api/*, and any custom-option-defining proto) is dropped, which is what
keeps its extensions out of the descriptor pool.
"""

import sys

from google.protobuf import descriptor_pb2

_WELL_KNOWN_PREFIX = "google/protobuf/"


def clear_enum(enum):
    # ClearField wipes the whole EnumOptions, including the standard allow_alias
    # flag. An enum with aliased values (two names on one number) is invalid
    # without it, and the generator's reparse rejects the descriptor. Strip the
    # custom/extension options but carry allow_alias through.
    allow_alias = enum.options.allow_alias
    enum.ClearField("options")
    if allow_alias:
        enum.options.allow_alias = True


def clear_message(message):
    message.ClearField("options")
    for field in message.field:
        field.ClearField("options")
    for nested in message.nested_type:
        clear_message(nested)
    for enum in message.enum_type:
        clear_enum(enum)


def strip(file_proto):
    deps = [d for d in file_proto.dependency if d.startswith(_WELL_KNOWN_PREFIX)]
    del file_proto.dependency[:]
    file_proto.dependency.extend(deps)
    del file_proto.public_dependency[:]
    del file_proto.weak_dependency[:]
    file_proto.ClearField("options")
    for message in file_proto.message_type:
        clear_message(message)
    for enum in file_proto.enum_type:
        clear_enum(enum)
    for service in file_proto.service:
        service.ClearField("options")
        for method in service.method:
            method.ClearField("options")


def main() -> None:
    targets = set(sys.argv[3:])

    source = descriptor_pb2.FileDescriptorSet()
    source.ParseFromString(open(sys.argv[1], "rb").read())

    stripped = {}
    needed_wkt = set()
    for file_proto in source.file:
        if file_proto.name in targets:
            strip(file_proto)
            needed_wkt.update(
                d for d in file_proto.dependency if d.startswith(_WELL_KNOWN_PREFIX)
            )
            stripped[file_proto.name] = file_proto

    # Source order is topological (a file's dependencies precede it), so keeping
    # it leaves every referenced well-known type ahead of the file that needs it.
    out = descriptor_pb2.FileDescriptorSet()
    for file_proto in source.file:
        if file_proto.name in stripped:
            out.file.append(stripped[file_proto.name])
        elif file_proto.name in needed_wkt:
            out.file.append(file_proto)

    open(sys.argv[2], "wb").write(out.SerializeToString())
    print("kept:", [f.name for f in out.file], file=sys.stderr)


if __name__ == "__main__":
    main()
