# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: proto/agents/logs.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from proto.base import sessions_pb2 as proto_dot_base_dot_sessions__pb2
from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x17proto/agents/logs.proto\x12\x0ev1.agents.logs\x1a\x19proto/base/sessions.proto\x1a\x1fgoogle/protobuf/timestamp.proto\"\xe2\x01\n\x03Log\x12*\n\x02\x61t\x18\x01 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x02\x61t\x12 \n\x0b\x61pplication\x18\x02 \x01(\tR\x0b\x61pplication\x12\x18\n\x07service\x18\x03 \x01(\tR\x07service\x12,\n\x04kind\x18\x04 \x01(\x0e\x32\x18.v1.agents.logs.Log.KindR\x04kind\x12\x18\n\x07message\x18\x05 \x01(\tR\x07message\"+\n\x04Kind\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05\x41GENT\x10\x01\x12\x0b\n\x07SERVICE\x10\x02\"f\n\x0fLogSessionGroup\x12*\n\x07session\x18\x01 \x01(\x0b\x32\x10.v1.base.SessionR\x07session\x12\'\n\x04logs\x18\x02 \x03(\x0b\x32\x13.v1.agents.logs.LogR\x04logsB\xb3\x01\n\x12\x63om.v1.agents.logsB\tLogsProtoP\x01Z8github.com/codefly-dev/core/generated/v1/go/proto/agents\xa2\x02\x03VAL\xaa\x02\x0eV1.Agents.Logs\xca\x02\x0eV1\\Agents\\Logs\xe2\x02\x1aV1\\Agents\\Logs\\GPBMetadata\xea\x02\x10V1::Agents::Logsb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'proto.agents.logs_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\022com.v1.agents.logsB\tLogsProtoP\001Z8github.com/codefly-dev/core/generated/v1/go/proto/agents\242\002\003VAL\252\002\016V1.Agents.Logs\312\002\016V1\\Agents\\Logs\342\002\032V1\\Agents\\Logs\\GPBMetadata\352\002\020V1::Agents::Logs'
  _globals['_LOG']._serialized_start=104
  _globals['_LOG']._serialized_end=330
  _globals['_LOG_KIND']._serialized_start=287
  _globals['_LOG_KIND']._serialized_end=330
  _globals['_LOGSESSIONGROUP']._serialized_start=332
  _globals['_LOGSESSIONGROUP']._serialized_end=434
# @@protoc_insertion_point(module_scope)
