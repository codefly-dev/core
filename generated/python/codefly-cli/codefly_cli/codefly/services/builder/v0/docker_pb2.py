# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/services/builder/v0/docker.proto
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
    'codefly/services/builder/v0/docker.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()




DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n(codefly/services/builder/v0/docker.proto\x12\x1b\x63odefly.services.builder.v0\"A\n\x12\x44ockerBuildContext\x12+\n\x11\x64ocker_repository\x18\x01 \x01(\tR\x10\x64ockerRepository\"+\n\x11\x44ockerBuildResult\x12\x16\n\x06images\x18\x01 \x03(\tR\x06imagesB\x84\x02\n\x1f\x63om.codefly.services.builder.v0B\x0b\x44ockerProtoP\x01ZDgithub.com/codefly-dev/core/generated/go/codefly/services/builder/v0\xa2\x02\x04\x43SBV\xaa\x02\x1b\x43odefly.Services.Builder.V0\xca\x02\x1b\x43odefly\\Services\\Builder\\V0\xe2\x02\'Codefly\\Services\\Builder\\V0\\GPBMetadata\xea\x02\x1e\x43odefly::Services::Builder::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.services.builder.v0.docker_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\037com.codefly.services.builder.v0B\013DockerProtoP\001ZDgithub.com/codefly-dev/core/generated/go/codefly/services/builder/v0\242\002\004CSBV\252\002\033Codefly.Services.Builder.V0\312\002\033Codefly\\Services\\Builder\\V0\342\002\'Codefly\\Services\\Builder\\V0\\GPBMetadata\352\002\036Codefly::Services::Builder::V0'
  _globals['_DOCKERBUILDCONTEXT']._serialized_start=73
  _globals['_DOCKERBUILDCONTEXT']._serialized_end=138
  _globals['_DOCKERBUILDRESULT']._serialized_start=140
  _globals['_DOCKERBUILDRESULT']._serialized_end=183
# @@protoc_insertion_point(module_scope)