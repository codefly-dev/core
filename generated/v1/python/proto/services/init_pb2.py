# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: proto/services/init.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from google.protobuf import empty_pb2 as google_dot_protobuf_dot_empty__pb2
from proto.base import endpoint_pb2 as proto_dot_base_dot_endpoint__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x19proto/services/init.proto\x12\x0bv1.services\x1a\x1bgoogle/protobuf/empty.proto\x1a\x19proto/base/endpoint.proto\"\x87\x01\n\nInitStatus\x12\x33\n\x05state\x18\x01 \x01(\x0e\x32\x1d.v1.services.InitStatus.StateR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"*\n\x05State\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05READY\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"#\n\x07Version\x12\x18\n\x07version\x18\x01 \x01(\tR\x07version\"}\n\x0fServiceIdentity\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12\x16\n\x06\x64omain\x18\x02 \x01(\tR\x06\x64omain\x12 \n\x0b\x61pplication\x18\x03 \x01(\tR\x0b\x61pplication\x12\x1c\n\tnamespace\x18\x04 \x01(\tR\tnamespace\"\xcd\x01\n\x0bInitRequest\x12\x14\n\x05\x64\x65\x62ug\x18\x01 \x01(\x08R\x05\x64\x65\x62ug\x12\x1a\n\x08location\x18\x02 \x01(\tR\x08location\x12\x38\n\x08identity\x18\x03 \x01(\x0b\x32\x1c.v1.services.ServiceIdentityR\x08identity\x12R\n\x19\x64\x65pendency_endpoint_group\x18\x04 \x01(\x0b\x32\x16.v1.base.EndpointGroupR\x17\x64\x65pendencyEndpointGroupB\xa5\x01\n\x0f\x63om.v1.servicesB\tInitProtoP\x01Z:github.com/codefly-dev/core/generated/v1/go/proto/services\xa2\x02\x03VSX\xaa\x02\x0bV1.Services\xca\x02\x0bV1\\Services\xe2\x02\x17V1\\Services\\GPBMetadata\xea\x02\x0cV1::Servicesb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'proto.services.init_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\017com.v1.servicesB\tInitProtoP\001Z:github.com/codefly-dev/core/generated/v1/go/proto/services\242\002\003VSX\252\002\013V1.Services\312\002\013V1\\Services\342\002\027V1\\Services\\GPBMetadata\352\002\014V1::Services'
  _globals['_INITSTATUS']._serialized_start=99
  _globals['_INITSTATUS']._serialized_end=234
  _globals['_INITSTATUS_STATE']._serialized_start=192
  _globals['_INITSTATUS_STATE']._serialized_end=234
  _globals['_VERSION']._serialized_start=236
  _globals['_VERSION']._serialized_end=271
  _globals['_SERVICEIDENTITY']._serialized_start=273
  _globals['_SERVICEIDENTITY']._serialized_end=398
  _globals['_INITREQUEST']._serialized_start=401
  _globals['_INITREQUEST']._serialized_end=606
# @@protoc_insertion_point(module_scope)
