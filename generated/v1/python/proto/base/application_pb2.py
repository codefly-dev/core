# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: proto/base/application.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from proto.base import service_pb2 as proto_dot_base_dot_service__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1cproto/base/application.proto\x12\x07v1.base\x1a\x18proto/base/service.proto\"\x8b\x01\n\x0b\x41pplication\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12 \n\x0b\x64\x65scription\x18\x02 \x01(\tR\x0b\x64\x65scription\x12\x18\n\x07project\x18\x03 \x01(\tR\x07project\x12,\n\x08services\x18\x04 \x03(\x0b\x32\x10.v1.base.ServiceR\x08servicesB\x94\x01\n\x0b\x63om.v1.baseB\x10\x41pplicationProtoP\x01Z6github.com/codefly-dev/core/generated/v1/go/proto/base\xa2\x02\x03VBX\xaa\x02\x07V1.Base\xca\x02\x07V1\\Base\xe2\x02\x13V1\\Base\\GPBMetadata\xea\x02\x08V1::Baseb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'proto.base.application_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\013com.v1.baseB\020ApplicationProtoP\001Z6github.com/codefly-dev/core/generated/v1/go/proto/base\242\002\003VBX\252\002\007V1.Base\312\002\007V1\\Base\342\002\023V1\\Base\\GPBMetadata\352\002\010V1::Base'
  _globals['_APPLICATION']._serialized_start=68
  _globals['_APPLICATION']._serialized_end=207
# @@protoc_insertion_point(module_scope)