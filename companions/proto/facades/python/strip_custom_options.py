"""Strip custom options + shared-proto deps from a buf image so the generated
message bindings embed only the module's own proto(s) themselves (plus the
well-known types they reference, which come from the protobuf runtime). Removes
the buf.validate, google.api.http, and any other option extension whose defining
file is not kept, so the generated `*_pb2` never registers a shared descriptor
into the global pool — the collision saas-sdk-python documents. Built-in options
(map_entry, allow_alias, packed, ...) are kept: they are part of what the
descriptor means, and no dropped file defines them.

Usage: codefly-proto-strip-options <in.binpb> <out.binpb> <proto>...

Each <proto> names a file the library will own, and the output is exactly those
files: what the library contains is the caller's decision, not this script's.
Import edges between them survive, because both ends are kept.

What this script does decide is whether that set is *coherent*. A file the
targets reach through a type reference — a message field, an rpc signature, an
extension — must have bindings too, or the descriptor is invalid. So the
reachable set is computed and checked against the declared targets, and an
undeclared file is a hard error naming it rather than a silent inclusion:
vendoring another library's protos is what re-registers a shared descriptor into
the global pool. Files reached only through options carry no type reference, so
they never enter that set and are dropped as before.

google/protobuf/* is the one exception, carried through unstripped: those
descriptors are compiled into the protobuf runtime itself. Everything else under
google/ (google/rpc, google/type, google/api) belongs to the pip package
googleapis-common-protos, which registers those exact file names at import; a
copy in a generated library collides with it, so referencing one is refused
outright instead of being vendored.
"""

import sys

from google.protobuf import descriptor_pb2

_WELL_KNOWN_PREFIX = "google/protobuf/"
# Same namespace, different owner: shipped by googleapis-common-protos, never
# by us.
_FOREIGN_RUNTIME_PREFIX = "google/"


def clear_custom_options(holder):
    """Strip the custom options from holder.options, keep the built-in ones.

    A custom option is an extension of a google.protobuf.*Options message. Its
    defining file is dropped from the output, so it must go: either it is
    registered (an extension field), unregistered (an unknown field — the usual
    case, since nothing here imports buf.validate or google.api), or not yet
    interpreted (uninterpreted_option). A built-in option is a declared field of
    the options message and changes what the descriptor means: map_entry is what
    makes a `map<K, V>` field a map rather than a repeated entry message,
    allow_alias is what makes two names on one number legal, packed changes the
    wire format. Clearing the whole options message used to drop those with the
    custom ones."""
    if not holder.HasField("options"):
        return
    options = holder.options
    for field, _ in options.ListFields():
        if field.is_extension:
            options.ClearExtension(field)
    options.ClearField("uninterpreted_option")
    options.DiscardUnknownFields()
    if options.ByteSize() == 0:
        holder.ClearField("options")


def clear_enum(enum):
    clear_custom_options(enum)
    for value in enum.value:
        clear_custom_options(value)


def clear_message(message):
    clear_custom_options(message)
    for field in message.field:
        clear_custom_options(field)
    for oneof in message.oneof_decl:
        clear_custom_options(oneof)
    for extension in message.extension:
        clear_custom_options(extension)
    for nested in message.nested_type:
        clear_message(nested)
    for enum in message.enum_type:
        clear_enum(enum)


def clear_file_options(file_proto):
    """Strip the custom options from every element of file_proto."""
    clear_custom_options(file_proto)
    for message in file_proto.message_type:
        clear_message(message)
    for enum in file_proto.enum_type:
        clear_enum(enum)
    for extension in file_proto.extension:
        clear_custom_options(extension)
    for service in file_proto.service:
        clear_custom_options(service)
        for method in service.method:
            clear_custom_options(method)


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
    """Types this file names in a field, an rpc signature, or an extension — the
    references that must resolve for its bindings to be a valid descriptor.
    Option usage is deliberately not walked: those references die with the
    options."""
    refs = []

    def add_extensions(extensions):
        for extension in extensions:
            refs.append(extension.extendee)
            if extension.type_name:
                refs.append(extension.type_name)

    def walk(message):
        for field in message.field:
            if field.type_name:
                refs.append(field.type_name)
        add_extensions(message.extension)
        for nested in message.nested_type:
            walk(nested)

    for message in file_proto.message_type:
        walk(message)
    add_extensions(file_proto.extension)
    for service in file_proto.service:
        for method in service.method:
            refs.append(method.input_type)
            refs.append(method.output_type)
    return refs


def reachable_files(files_by_name, targets):
    """Every non-well-known file the targets reach through a type reference,
    transitively. Well-known types are excluded: they ship with the protobuf
    runtime and are carried through unstripped."""
    owners = type_owners(files_by_name.values())
    reached = set()
    seen = set()
    frontier = [name for name in targets if name in files_by_name]
    while frontier:
        name = frontier.pop()
        if name in seen:
            continue
        seen.add(name)
        for ref in referenced_types(files_by_name[name]):
            owner = owners.get(ref)
            if owner is None or owner.startswith(_WELL_KNOWN_PREFIX):
                continue
            reached.add(owner)
            if owner not in seen:
                frontier.append(owner)
    return reached


def check_targets(files_by_name, targets):
    """Refuse a target set the descriptor cannot be built from. Raises with the
    offending files named; returns nothing when the set is coherent."""
    declared = set(targets)
    reached = reachable_files(files_by_name, targets)

    foreign = sorted(
        name
        for name in declared | reached
        if name.startswith(_FOREIGN_RUNTIME_PREFIX)
        and not name.startswith(_WELL_KNOWN_PREFIX)
    )
    if foreign:
        raise SystemExit(
            "codefly-proto-strip-options: refusing to generate bindings for protos "
            "owned by googleapis-common-protos, which registers these same file "
            "names at import:\n  " + "\n  ".join(foreign) + "\nA copy inside the "
            "generated library collides with it in the descriptor pool. Drop the "
            "reference, or generate against a contract that does not use it."
        )

    undeclared = sorted(reached - declared)
    if undeclared:
        raise SystemExit(
            "codefly-proto-strip-options: the module's protos reference types "
            "defined in files that were not declared as targets:\n  "
            + "\n  ".join(undeclared)
            + "\nDeclare them so the library owns their bindings too, or drop the "
            "reference. Silently pulling them in would put another library's "
            "descriptors in this one."
        )


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
    clear_file_options(file_proto)


def well_known_closure(files_by_name, kept):
    """The well-known types the kept files need, plus the ones those pull in.
    google/protobuf/api.proto imports source_context.proto and type.proto, so
    emitting it alone would leave the descriptor unbuildable."""
    needed = set()
    frontier = [
        dep
        for name in kept
        for dep in files_by_name[name].dependency
        if dep.startswith(_WELL_KNOWN_PREFIX)
    ]
    while frontier:
        name = frontier.pop()
        if name in needed or name not in files_by_name:
            continue
        needed.add(name)
        frontier.extend(
            dep
            for dep in files_by_name[name].dependency
            if dep.startswith(_WELL_KNOWN_PREFIX)
        )
    return needed


def main() -> None:
    source = descriptor_pb2.FileDescriptorSet()
    source.ParseFromString(open(sys.argv[1], "rb").read())

    files_by_name = {file_proto.name: file_proto for file_proto in source.file}
    targets = sys.argv[3:]
    check_targets(files_by_name, targets)

    kept = {name for name in targets if name in files_by_name}
    for name in kept:
        strip(files_by_name[name], kept)
    needed_wkt = well_known_closure(files_by_name, kept)

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
