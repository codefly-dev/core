# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/base/v0/configuration.proto
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
    'codefly/base/v0/configuration.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from codefly.base.v0 import scope_pb2 as codefly_dot_base_dot_v0_dot_scope__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n#codefly/base/v0/configuration.proto\x12\x0f\x63odefly.base.v0\x1a\x1b\x63odefly/base/v0/scope.proto\"T\n\x12\x43onfigurationValue\x12\x10\n\x03key\x18\x01 \x01(\tR\x03key\x12\x14\n\x05value\x18\x02 \x01(\tR\x05value\x12\x16\n\x06secret\x18\x03 \x01(\x08R\x06secret\"\x86\x01\n\x18\x43onfigurationInformation\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12V\n\x14\x63onfiguration_values\x18\x04 \x03(\x0b\x32#.codefly.base.v0.ConfigurationValueR\x13\x63onfigurationValues\"\xb2\x01\n\rConfiguration\x12\x16\n\x06origin\x18\x01 \x01(\tR\x06origin\x12H\n\x0fruntime_context\x18\x02 \x01(\x0b\x32\x1f.codefly.base.v0.RuntimeContextR\x0eruntimeContext\x12?\n\x05infos\x18\x03 \x03(\x0b\x32).codefly.base.v0.ConfigurationInformationR\x05infosB\xc1\x01\n\x13\x63om.codefly.base.v0B\x12\x43onfigurationProtoP\x01Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\xa2\x02\x03\x43\x42V\xaa\x02\x0f\x43odefly.Base.V0\xca\x02\x0f\x43odefly\\Base\\V0\xe2\x02\x1b\x43odefly\\Base\\V0\\GPBMetadata\xea\x02\x11\x43odefly::Base::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.base.v0.configuration_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\023com.codefly.base.v0B\022ConfigurationProtoP\001Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\242\002\003CBV\252\002\017Codefly.Base.V0\312\002\017Codefly\\Base\\V0\342\002\033Codefly\\Base\\V0\\GPBMetadata\352\002\021Codefly::Base::V0'
  _globals['_CONFIGURATIONVALUE']._serialized_start=85
  _globals['_CONFIGURATIONVALUE']._serialized_end=169
  _globals['_CONFIGURATIONINFORMATION']._serialized_start=172
  _globals['_CONFIGURATIONINFORMATION']._serialized_end=306
  _globals['_CONFIGURATION']._serialized_start=309
  _globals['_CONFIGURATION']._serialized_end=487
# @@protoc_insertion_point(module_scope)
