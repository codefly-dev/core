# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/actions/v0/workspace.proto
# Protobuf Python Version: 5.28.3
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import runtime_version as _runtime_version
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
_runtime_version.ValidateProtobufRuntimeVersion(
    _runtime_version.Domain.PUBLIC,
    5,
    28,
    3,
    '',
    'codefly/actions/v0/workspace.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from buf.validate import validate_pb2 as buf_dot_validate_dot_validate__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\"codefly/actions/v0/workspace.proto\x12\x12\x63odefly.actions.v0\x1a\x1b\x62uf/validate/validate.proto\"\xb6\x01\n\x0cNewWorkspace\x12\x12\n\x04kind\x18\x01 \x01(\tR\x04kind\x12\x1d\n\x04name\x18\x02 \x01(\tB\t\xbaH\x06r\x04\x10\x03\x18\x32R\x04name\x12 \n\x0b\x64\x65scription\x18\x03 \x01(\tR\x0b\x64\x65scription\x12\x1b\n\x04path\x18\x04 \x01(\tB\x07\xbaH\x04r\x02\x10\x03R\x04path\x12\x34\n\x06layout\x18\x05 \x01(\tB\x1c\xbaH\x19r\x17R\x04\x66latR\x07modulesR\x06hybridR\x06layoutB\xcf\x01\n\x16\x63om.codefly.actions.v0B\x0eWorkspaceProtoP\x01Z;github.com/codefly-dev/core/generated/go/codefly/actions/v0\xa2\x02\x03\x43\x41V\xaa\x02\x12\x43odefly.Actions.V0\xca\x02\x12\x43odefly\\Actions\\V0\xe2\x02\x1e\x43odefly\\Actions\\V0\\GPBMetadata\xea\x02\x14\x43odefly::Actions::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.actions.v0.workspace_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\026com.codefly.actions.v0B\016WorkspaceProtoP\001Z;github.com/codefly-dev/core/generated/go/codefly/actions/v0\242\002\003CAV\252\002\022Codefly.Actions.V0\312\002\022Codefly\\Actions\\V0\342\002\036Codefly\\Actions\\V0\\GPBMetadata\352\002\024Codefly::Actions::V0'
  _globals['_NEWWORKSPACE'].fields_by_name['name']._loaded_options = None
  _globals['_NEWWORKSPACE'].fields_by_name['name']._serialized_options = b'\272H\006r\004\020\003\0302'
  _globals['_NEWWORKSPACE'].fields_by_name['path']._loaded_options = None
  _globals['_NEWWORKSPACE'].fields_by_name['path']._serialized_options = b'\272H\004r\002\020\003'
  _globals['_NEWWORKSPACE'].fields_by_name['layout']._loaded_options = None
  _globals['_NEWWORKSPACE'].fields_by_name['layout']._serialized_options = b'\272H\031r\027R\004flatR\007modulesR\006hybrid'
  _globals['_NEWWORKSPACE']._serialized_start=88
  _globals['_NEWWORKSPACE']._serialized_end=270
# @@protoc_insertion_point(module_scope)
