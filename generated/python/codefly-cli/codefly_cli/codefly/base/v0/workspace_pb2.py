# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/base/v0/workspace.proto
# Protobuf Python Version: 5.28.2
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
    2,
    '',
    'codefly/base/v0/workspace.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from buf.validate import validate_pb2 as buf_dot_validate_dot_validate__pb2
from codefly.base.v0 import module_pb2 as codefly_dot_base_dot_v0_dot_module__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1f\x63odefly/base/v0/workspace.proto\x12\x0f\x63odefly.base.v0\x1a\x1b\x62uf/validate/validate.proto\x1a\x1c\x63odefly/base/v0/module.proto\"\xca\x01\n\tWorkspace\x12\x32\n\x04name\x18\x01 \x01(\tB\x1e\xbaH\x1br\x19\x10\x03\x18\x19\x32\x0c^[a-z0-9-]+$h\x01\xba\x01\x02--R\x04name\x12 \n\x0b\x64\x65scription\x18\x02 \x01(\tR\x0b\x64\x65scription\x12\x31\n\x07modules\x18\x03 \x03(\x0b\x32\x17.codefly.base.v0.ModuleR\x07modules\x12\x34\n\x06layout\x18\x04 \x01(\tB\x1c\xbaH\x19r\x17R\x04\x66latR\x07modulesR\x06hybridR\x06layout\"\xc0\x01\n\x10ManagedWorkspace\x12;\n\x0forganization_id\x18\x01 \x01(\tB\x12\xbaH\x0fr\r2\x0b^[a-z]{10}$R\x0eorganizationId\x12\x35\n\x0cworkspace_id\x18\x02 \x01(\tB\x12\xbaH\x0fr\r2\x0b^[a-z]{10}$R\x0bworkspaceId\x12\x38\n\tworkspace\x18\x03 \x01(\x0b\x32\x1a.codefly.base.v0.WorkspaceR\tworkspaceB\xbd\x01\n\x13\x63om.codefly.base.v0B\x0eWorkspaceProtoP\x01Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\xa2\x02\x03\x43\x42V\xaa\x02\x0f\x43odefly.Base.V0\xca\x02\x0f\x43odefly\\Base\\V0\xe2\x02\x1b\x43odefly\\Base\\V0\\GPBMetadata\xea\x02\x11\x43odefly::Base::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.base.v0.workspace_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\023com.codefly.base.v0B\016WorkspaceProtoP\001Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\242\002\003CBV\252\002\017Codefly.Base.V0\312\002\017Codefly\\Base\\V0\342\002\033Codefly\\Base\\V0\\GPBMetadata\352\002\021Codefly::Base::V0'
  _globals['_WORKSPACE'].fields_by_name['name']._loaded_options = None
  _globals['_WORKSPACE'].fields_by_name['name']._serialized_options = b'\272H\033r\031\020\003\030\0312\014^[a-z0-9-]+$h\001\272\001\002--'
  _globals['_WORKSPACE'].fields_by_name['layout']._loaded_options = None
  _globals['_WORKSPACE'].fields_by_name['layout']._serialized_options = b'\272H\031r\027R\004flatR\007modulesR\006hybrid'
  _globals['_MANAGEDWORKSPACE'].fields_by_name['organization_id']._loaded_options = None
  _globals['_MANAGEDWORKSPACE'].fields_by_name['organization_id']._serialized_options = b'\272H\017r\r2\013^[a-z]{10}$'
  _globals['_MANAGEDWORKSPACE'].fields_by_name['workspace_id']._loaded_options = None
  _globals['_MANAGEDWORKSPACE'].fields_by_name['workspace_id']._serialized_options = b'\272H\017r\r2\013^[a-z]{10}$'
  _globals['_WORKSPACE']._serialized_start=112
  _globals['_WORKSPACE']._serialized_end=314
  _globals['_MANAGEDWORKSPACE']._serialized_start=317
  _globals['_MANAGEDWORKSPACE']._serialized_end=509
# @@protoc_insertion_point(module_scope)
