# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/cli/v0/utils.proto
# Protobuf Python Version: 5.27.2
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
    2,
    '',
    'codefly/cli/v0/utils.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()




DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1a\x63odefly/cli/v0/utils.proto\x12\x0e\x63odefly.cli.v0\"[\n\x08\x46ileInfo\x12\x12\n\x04path\x18\x01 \x01(\tR\x04path\x12\x18\n\x07\x63ontent\x18\x02 \x01(\x0cR\x07\x63ontent\x12!\n\x0cis_directory\x18\x03 \x01(\x08R\x0bisDirectory\"B\n\x10\x44irectoryRequest\x12.\n\x05\x66iles\x18\x01 \x03(\x0b\x32\x18.codefly.cli.v0.FileInfoR\x05\x66iles\"\x13\n\x11\x44irectoryResponseB\xb3\x01\n\x12\x63om.codefly.cli.v0B\nUtilsProtoP\x01Z7github.com/codefly-dev/core/generated/go/codefly/cli/v0\xa2\x02\x03\x43\x43V\xaa\x02\x0e\x43odefly.Cli.V0\xca\x02\x0e\x43odefly\\Cli\\V0\xe2\x02\x1a\x43odefly\\Cli\\V0\\GPBMetadata\xea\x02\x10\x43odefly::Cli::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.cli.v0.utils_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\022com.codefly.cli.v0B\nUtilsProtoP\001Z7github.com/codefly-dev/core/generated/go/codefly/cli/v0\242\002\003CCV\252\002\016Codefly.Cli.V0\312\002\016Codefly\\Cli\\V0\342\002\032Codefly\\Cli\\V0\\GPBMetadata\352\002\020Codefly::Cli::V0'
  _globals['_FILEINFO']._serialized_start=46
  _globals['_FILEINFO']._serialized_end=137
  _globals['_DIRECTORYREQUEST']._serialized_start=139
  _globals['_DIRECTORYREQUEST']._serialized_end=205
  _globals['_DIRECTORYRESPONSE']._serialized_start=207
  _globals['_DIRECTORYRESPONSE']._serialized_end=226
# @@protoc_insertion_point(module_scope)
