# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: services/agent/v0/cyclonedx.proto
# Protobuf Python Version: 5.27.0
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import runtime_version as _runtime_version
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
_runtime_version.ValidateProtobufRuntimeVersion(
    _runtime_version.Domain.PUBLIC,
    5,
    27,
    0,
    '',
    'services/agent/v0/cyclonedx.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()




DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n!services/agent/v0/cyclonedx.proto\x12\tcyclonedx\"\x91\x01\n\tComponent\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12\x18\n\x07version\x18\x02 \x01(\tR\x07version\x12,\n\x04type\x18\x03 \x01(\x0e\x32\x18.cyclonedx.ComponentTypeR\x04type\x12\x14\n\x05group\x18\x04 \x01(\tR\x05group\x12\x12\n\x04purl\x18\x05 \x01(\tR\x04purl\"\xb9\x01\n\x03\x42om\x12\x1c\n\tbomFormat\x18\x01 \x01(\tR\tbomFormat\x12 \n\x0bspecVersion\x18\x02 \x01(\tR\x0bspecVersion\x12\"\n\x0cserialNumber\x18\x03 \x01(\tR\x0cserialNumber\x12\x18\n\x07version\x18\x04 \x01(\x05R\x07version\x12\x34\n\ncomponents\x18\x05 \x03(\x0b\x32\x14.cyclonedx.ComponentR\ncomponents*F\n\rComponentType\x12\x0b\n\x07LIBRARY\x10\x00\x12\r\n\tFRAMEWORK\x10\x01\x12\n\n\x06MODULE\x10\x02\x12\r\n\tCONTAINER\x10\x03\x42\x9f\x01\n\rcom.cyclonedxB\x0e\x43yclonedxProtoP\x01Z:github.com/codefly-dev/core/generated/go/services/agent/v0\xa2\x02\x03\x43XX\xaa\x02\tCyclonedx\xca\x02\tCyclonedx\xe2\x02\x15\x43yclonedx\\GPBMetadata\xea\x02\tCyclonedxb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'services.agent.v0.cyclonedx_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\rcom.cyclonedxB\016CyclonedxProtoP\001Z:github.com/codefly-dev/core/generated/go/services/agent/v0\242\002\003CXX\252\002\tCyclonedx\312\002\tCyclonedx\342\002\025Cyclonedx\\GPBMetadata\352\002\tCyclonedx'
  _globals['_COMPONENTTYPE']._serialized_start=384
  _globals['_COMPONENTTYPE']._serialized_end=454
  _globals['_COMPONENT']._serialized_start=49
  _globals['_COMPONENT']._serialized_end=194
  _globals['_BOM']._serialized_start=197
  _globals['_BOM']._serialized_end=382
# @@protoc_insertion_point(module_scope)
