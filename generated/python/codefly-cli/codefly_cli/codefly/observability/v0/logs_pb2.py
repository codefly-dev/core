# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/observability/v0/logs.proto
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
    'codefly/observability/v0/logs.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from codefly.base.v0 import endpoint_pb2 as codefly_dot_base_dot_v0_dot_endpoint__pb2
from codefly.observability.v0 import sessions_pb2 as codefly_dot_observability_dot_v0_dot_sessions__pb2
from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n#codefly/observability/v0/logs.proto\x12\x18\x63odefly.observability.v0\x1a\x1e\x63odefly/base/v0/endpoint.proto\x1a\'codefly/observability/v0/sessions.proto\x1a\x1fgoogle/protobuf/timestamp.proto\"\x91\x01\n\x03Log\x12*\n\x02\x61t\x18\x01 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x02\x61t\x12\x16\n\x06module\x18\x02 \x01(\tR\x06module\x12\x18\n\x07service\x18\x03 \x01(\tR\x07service\x12\x12\n\x04kind\x18\x04 \x01(\tR\x04kind\x12\x18\n\x07message\x18\x05 \x01(\tR\x07message\"\x81\x01\n\x0fLogSessionGroup\x12;\n\x07session\x18\x01 \x01(\x0b\x32!.codefly.observability.v0.SessionR\x07session\x12\x31\n\x04logs\x18\x02 \x03(\x0b\x32\x1d.codefly.observability.v0.LogR\x04logs\"h\n\nLogRequest\x12.\n\x04\x66rom\x18\x01 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x04\x66rom\x12*\n\x02to\x18\x02 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x02to\"P\n\x0bLogResponse\x12\x41\n\x06groups\x18\x01 \x03(\x0b\x32).codefly.observability.v0.LogSessionGroupR\x06groupsB\xee\x01\n\x1c\x63om.codefly.observability.v0B\tLogsProtoP\x01ZAgithub.com/codefly-dev/core/generated/go/codefly/observability/v0\xa2\x02\x03\x43OV\xaa\x02\x18\x43odefly.Observability.V0\xca\x02\x18\x43odefly\\Observability\\V0\xe2\x02$Codefly\\Observability\\V0\\GPBMetadata\xea\x02\x1a\x43odefly::Observability::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.observability.v0.logs_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\034com.codefly.observability.v0B\tLogsProtoP\001ZAgithub.com/codefly-dev/core/generated/go/codefly/observability/v0\242\002\003COV\252\002\030Codefly.Observability.V0\312\002\030Codefly\\Observability\\V0\342\002$Codefly\\Observability\\V0\\GPBMetadata\352\002\032Codefly::Observability::V0'
  _globals['_LOG']._serialized_start=172
  _globals['_LOG']._serialized_end=317
  _globals['_LOGSESSIONGROUP']._serialized_start=320
  _globals['_LOGSESSIONGROUP']._serialized_end=449
  _globals['_LOGREQUEST']._serialized_start=451
  _globals['_LOGREQUEST']._serialized_end=555
  _globals['_LOGRESPONSE']._serialized_start=557
  _globals['_LOGRESPONSE']._serialized_end=637
# @@protoc_insertion_point(module_scope)