# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/base/v0/module.proto
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
    'codefly/base/v0/module.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from buf.validate import validate_pb2 as buf_dot_validate_dot_validate__pb2
from codefly.base.v0 import service_pb2 as codefly_dot_base_dot_v0_dot_service__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1c\x63odefly/base/v0/module.proto\x12\x0f\x63odefly.base.v0\x1a\x1b\x62uf/validate/validate.proto\x1a\x1d\x63odefly/base/v0/service.proto\"\x99\x01\n\x06Module\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12 \n\x0b\x64\x65scription\x18\x02 \x01(\tR\x0b\x64\x65scription\x12\x34\n\x08services\x18\x04 \x03(\x0b\x32\x18.codefly.base.v0.ServiceR\x08services\x12#\n\rservice_entry\x18\x05 \x01(\tR\x0cserviceEntry\"d\n\rManagedModule\x12\"\n\x02id\x18\x01 \x01(\tB\x12\xbaH\x0fr\r2\x0b^[a-z]{10}$R\x02id\x12/\n\x06module\x18\x02 \x01(\x0b\x32\x17.codefly.base.v0.ModuleR\x06moduleB\xba\x01\n\x13\x63om.codefly.base.v0B\x0bModuleProtoP\x01Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\xa2\x02\x03\x43\x42V\xaa\x02\x0f\x43odefly.Base.V0\xca\x02\x0f\x43odefly\\Base\\V0\xe2\x02\x1b\x43odefly\\Base\\V0\\GPBMetadata\xea\x02\x11\x43odefly::Base::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.base.v0.module_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\023com.codefly.base.v0B\013ModuleProtoP\001Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\242\002\003CBV\252\002\017Codefly.Base.V0\312\002\017Codefly\\Base\\V0\342\002\033Codefly\\Base\\V0\\GPBMetadata\352\002\021Codefly::Base::V0'
  _globals['_MANAGEDMODULE'].fields_by_name['id']._loaded_options = None
  _globals['_MANAGEDMODULE'].fields_by_name['id']._serialized_options = b'\272H\017r\r2\013^[a-z]{10}$'
  _globals['_MODULE']._serialized_start=110
  _globals['_MODULE']._serialized_end=263
  _globals['_MANAGEDMODULE']._serialized_start=265
  _globals['_MANAGEDMODULE']._serialized_end=365
# @@protoc_insertion_point(module_scope)
