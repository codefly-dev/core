# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: proto/observability/logs.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from proto.base import endpoint_pb2 as proto_dot_base_dot_endpoint__pb2
from proto.agents import logs_pb2 as proto_dot_agents_dot_logs__pb2
from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1eproto/observability/logs.proto\x12\x10v1.observability\x1a\x19proto/base/endpoint.proto\x1a\x17proto/agents/logs.proto\x1a\x1fgoogle/protobuf/timestamp.proto\"h\n\nLogRequest\x12.\n\x04\x66rom\x18\x01 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x04\x66rom\x12*\n\x02to\x18\x02 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x02to\"F\n\x0bLogResponse\x12\x37\n\x06groups\x18\x01 \x03(\x0b\x32\x1f.v1.agents.logs.LogSessionGroupR\x06groupsB\xc3\x01\n\x14\x63om.v1.observabilityB\tLogsProtoP\x01Z?github.com/codefly-dev/core/generated/v1/go/proto/observability\xa2\x02\x03VOX\xaa\x02\x10V1.Observability\xca\x02\x10V1\\Observability\xe2\x02\x1cV1\\Observability\\GPBMetadata\xea\x02\x11V1::Observabilityb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'proto.observability.logs_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\024com.v1.observabilityB\tLogsProtoP\001Z?github.com/codefly-dev/core/generated/v1/go/proto/observability\242\002\003VOX\252\002\020V1.Observability\312\002\020V1\\Observability\342\002\034V1\\Observability\\GPBMetadata\352\002\021V1::Observability'
  _globals['_LOGREQUEST']._serialized_start=137
  _globals['_LOGREQUEST']._serialized_end=241
  _globals['_LOGRESPONSE']._serialized_start=243
  _globals['_LOGRESPONSE']._serialized_end=313
# @@protoc_insertion_point(module_scope)
