# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/base/v0/environment.proto
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
    'codefly/base/v0/environment.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from buf.validate import validate_pb2 as buf_dot_validate_dot_validate__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n!codefly/base/v0/environment.proto\x12\x0f\x63odefly.base.v0\x1a\x1b\x62uf/validate/validate.proto\"C\n\x0b\x45nvironment\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12 \n\x0b\x64\x65scription\x18\x02 \x01(\tR\x0b\x64\x65scription\"8\n\x12ManagedEnvironment\x12\"\n\x02id\x18\x01 \x01(\tB\x12\xbaH\x0fr\r2\x0b^[a-z]{10}$R\x02idB\xbf\x01\n\x13\x63om.codefly.base.v0B\x10\x45nvironmentProtoP\x01Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\xa2\x02\x03\x43\x42V\xaa\x02\x0f\x43odefly.Base.V0\xca\x02\x0f\x43odefly\\Base\\V0\xe2\x02\x1b\x43odefly\\Base\\V0\\GPBMetadata\xea\x02\x11\x43odefly::Base::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.base.v0.environment_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\023com.codefly.base.v0B\020EnvironmentProtoP\001Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\242\002\003CBV\252\002\017Codefly.Base.V0\312\002\017Codefly\\Base\\V0\342\002\033Codefly\\Base\\V0\\GPBMetadata\352\002\021Codefly::Base::V0'
  _globals['_MANAGEDENVIRONMENT'].fields_by_name['id']._loaded_options = None
  _globals['_MANAGEDENVIRONMENT'].fields_by_name['id']._serialized_options = b'\272H\017r\r2\013^[a-z]{10}$'
  _globals['_ENVIRONMENT']._serialized_start=83
  _globals['_ENVIRONMENT']._serialized_end=150
  _globals['_MANAGEDENVIRONMENT']._serialized_start=152
  _globals['_MANAGEDENVIRONMENT']._serialized_end=208
# @@protoc_insertion_point(module_scope)