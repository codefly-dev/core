"""Strip custom options + shared-proto deps from a buf image so the generated
message bindings embed only the module's own proto(s) themselves (plus the
well-known types they reference, which come from the protobuf runtime). Removes
the buf.validate, google.api.http, and any other option extension whose defining
file is not kept, so the generated `*_pb2` never registers a shared descriptor
into the global pool — the collision saas-sdk-python documents.

Usage: codefly-proto-strip-options <in.binpb> <out.binpb> <proto>...

Each <proto> names a file the module owns. The output keeps those files and
every file that defines a type they reference, transitively: a message field or
an rpc request/response resolving into another proto means that proto's bindings
must exist too, or the descriptor is invalid. Files reachable only through
options (buf/validate/*, google/api/*, org-shared option protos) carry no type
reference, so the closure drops them, which is what keeps their extensions out
of the descriptor pool.
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
    for value in enum.value:
        value.ClearField("options")


def clear_message(message):
    message.ClearField("options")
    for field in message.field:
        field.ClearField("options")
    for oneof in message.oneof_decl:
        oneof.ClearField("options")
    for extension in message.extension:
        extension.ClearField("options")
    for nested in message.nested_type:
        clear_message(nested)
    for enum in message.enum_type:
        clear_enum(enum)


def type_owners(files):
    """Fully-qualified message and enum name -> the file that defines it."""
    owners = {}

    def walk(prefix, messages, file_name):
        for message in messages:
            full = f"{prefix}.{message.name}"
            owners[full] = file_name
            for enum in message.enum_type:
                owners[f"{full}.{enum.name}"] = file_name
            walk(full, message.nested_type, file_name)

    for file_proto in files:
        prefix = f".{file_proto.package}" if file_proto.package else ""
        for enum in file_proto.enum_type:
            owners[f"{prefix}.{enum.name}"] = file_proto.name
        walk(prefix, file_proto.message_type, file_proto.name)
    return owners


def referenced_types(file_proto):
    """Types this file names in a field or an rpc signature — the references
    that must resolve for its bindings to be a valid descriptor. Option usage is
    deliberately not walked: those references die with the options."""
    refs = []

    def walk(message):
        for field in message.field:
            if field.type_name:
                refs.append(field.type_name)
        for nested in message.nested_type:
            walk(nested)

    for message in file_proto.message_type:
        walk(message)
    for service in file_proto.service:
        for method in service.method:
            refs.append(method.input_type)
            refs.append(method.output_type)
    return refs


def closure(files_by_name, targets):
    """The targets plus every file defining a type they reach, transitively.
    Well-known types are excluded: they ship with the protobuf runtime and are
    carried through unstripped."""
    owners = type_owners(files_by_name.values())
    kept = set()
    frontier = [name for name in targets if name in files_by_name]
    while frontier:
        name = frontier.pop()
        if name in kept:
            continue
        kept.add(name)
        for ref in referenced_types(files_by_name[name]):
            owner = owners.get(ref)
            if owner and owner not in kept and not owner.startswith(_WELL_KNOWN_PREFIX):
                frontier.append(owner)
    return kept


def strip(file_proto, kept):
    deps = [
        d
        for d in file_proto.dependency
        if d.startswith(_WELL_KNOWN_PREFIX) or d in kept
    ]
    del file_proto.dependency[:]
    file_proto.dependency.extend(deps)
    del file_proto.public_dependency[:]
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


def main() -> None:
    source = descriptor_pb2.FileDescriptorSet()
    source.ParseFromString(open(sys.argv[1], "rb").read())

    files_by_name = {file_proto.name: file_proto for file_proto in source.file}
    kept = closure(files_by_name, sys.argv[3:])

    needed_wkt = set()
    for name in kept:
        file_proto = files_by_name[name]
        strip(file_proto, kept)
        needed_wkt.update(
            d for d in file_proto.dependency if d.startswith(_WELL_KNOWN_PREFIX)
        )

    # Source order is topological (a file's dependencies precede it), so keeping
    # it leaves every referenced file ahead of the file that needs it.
    out = descriptor_pb2.FileDescriptorSet()
    for file_proto in source.file:
        if file_proto.name in kept or file_proto.name in needed_wkt:
            out.file.append(file_proto)

    open(sys.argv[2], "wb").write(out.SerializeToString())
    print("kept:", [f.name for f in out.file], file=sys.stderr)


if __name__ == "__main__":
    main()
