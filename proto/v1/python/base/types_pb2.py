# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: base/types.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x10\x62\x61se/types.proto\x12\x07v1.base\x1a\x1fgoogle/protobuf/timestamp.proto\"9\n\x0fProjectSnapshot\x12\x12\n\x04uuid\x18\x01 \x01(\tR\x04uuid\x12\x12\n\x04name\x18\x02 \x01(\tR\x04name\"q\n\x13\x41pplicationSnapshot\x12\x12\n\x04uuid\x18\x01 \x01(\tR\x04uuid\x12\x12\n\x04name\x18\x02 \x01(\tR\x04name\x12\x32\n\x07project\x18\x03 \x01(\x0b\x32\x18.v1.base.ProjectSnapshotR\x07project\"\xaf\x01\n\x0fPartialSnapshot\x12\x12\n\x04uuid\x18\x01 \x01(\tR\x04uuid\x12\x12\n\x04name\x18\x02 \x01(\tR\x04name\x12\x32\n\x07project\x18\x03 \x01(\x0b\x32\x18.v1.base.ProjectSnapshotR\x07project\x12@\n\x0c\x61pplications\x18\x04 \x03(\x0b\x32\x1c.v1.base.ApplicationSnapshotR\x0c\x61pplications\"\xcc\x01\n\x07Session\x12\x12\n\x04uuid\x18\x01 \x01(\tR\x04uuid\x12*\n\x02\x61t\x18\x02 \x01(\x0b\x32\x1a.google.protobuf.TimestampR\x02\x61t\x12\x34\n\x07partial\x18\x03 \x01(\x0b\x32\x18.v1.base.PartialSnapshotH\x00R\x07partial\x12@\n\x0b\x61pplication\x18\x04 \x01(\x0b\x32\x1c.v1.base.ApplicationSnapshotH\x00R\x0b\x61pplicationB\t\n\x07sessionB\x84\x01\n\x0b\x63om.v1.baseB\nTypesProtoP\x01Z,github.com/codefly-dev/core/proto/v1/go/base\xa2\x02\x03VBX\xaa\x02\x07V1.Base\xca\x02\x07V1\\Base\xe2\x02\x13V1\\Base\\GPBMetadata\xea\x02\x08V1::Baseb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'base.types_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\013com.v1.baseB\nTypesProtoP\001Z,github.com/codefly-dev/core/proto/v1/go/base\242\002\003VBX\252\002\007V1.Base\312\002\007V1\\Base\342\002\023V1\\Base\\GPBMetadata\352\002\010V1::Base'
  _globals['_PROJECTSNAPSHOT']._serialized_start=62
  _globals['_PROJECTSNAPSHOT']._serialized_end=119
  _globals['_APPLICATIONSNAPSHOT']._serialized_start=121
  _globals['_APPLICATIONSNAPSHOT']._serialized_end=234
  _globals['_PARTIALSNAPSHOT']._serialized_start=237
  _globals['_PARTIALSNAPSHOT']._serialized_end=412
  _globals['_SESSION']._serialized_start=415
  _globals['_SESSION']._serialized_end=619
# @@protoc_insertion_point(module_scope)