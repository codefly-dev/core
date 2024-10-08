# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: codefly/base/v0/endpoint.proto
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
    'codefly/base/v0/endpoint.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from buf.validate import validate_pb2 as buf_dot_validate_dot_validate__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1e\x63odefly/base/v0/endpoint.proto\x12\x0f\x63odefly.base.v0\x1a\x1b\x62uf/validate/validate.proto\"\xfa\x02\n\x08\x45ndpoint\x12)\n\x04name\x18\x01 \x01(\tB\x15\xbaH\x12r\x10\x10\x03\x18\x14\x32\x08^[a-z]+$h\x01R\x04name\x12\x38\n\x07service\x18\x02 \x01(\tB\x1e\xbaH\x1br\x19\x10\x03\x18\x19\x32\x0c^[a-z0-9-]+$h\x01\xba\x01\x02--R\x07service\x12\x36\n\x06module\x18\x03 \x01(\tB\x1e\xbaH\x1br\x19\x10\x03\x18\x19\x32\x0c^[a-z0-9-]+$h\x01\xba\x01\x02--R\x06module\x12 \n\x0b\x64\x65scription\x18\x04 \x01(\tR\x0b\x64\x65scription\x12H\n\nvisibility\x18\x05 \x01(\tB(\xbaH%r#R\x08\x65xternalR\x06publicR\x06moduleR\x07privateR\nvisibility\x12.\n\x03\x61pi\x18\x06 \x01(\tB\x1c\xbaH\x19r\x17R\x04httpR\x04grpcR\x03tcpR\x04restR\x03\x61pi\x12\x35\n\x0b\x61pi_details\x18\x07 \x01(\x0b\x32\x14.codefly.base.v0.APIR\napiDetails\"\xcb\x01\n\x03\x41PI\x12+\n\x03tcp\x18\x01 \x01(\x0b\x32\x17.codefly.base.v0.TcpAPIH\x00R\x03tcp\x12.\n\x04http\x18\x02 \x01(\x0b\x32\x18.codefly.base.v0.HttpAPIH\x00R\x04http\x12.\n\x04rest\x18\x03 \x01(\x0b\x32\x18.codefly.base.v0.RestAPIH\x00R\x04rest\x12.\n\x04grpc\x18\x04 \x01(\x0b\x32\x18.codefly.base.v0.GrpcAPIH\x00R\x04grpcB\x07\n\x05value\"X\n\x0eRestRouteGroup\x12\x12\n\x04path\x18\x01 \x01(\tR\x04path\x12\x32\n\x06routes\x18\x02 \x03(\x0b\x32\x1a.codefly.base.v0.RestRouteR\x06routes\"T\n\tRestRoute\x12\x12\n\x04path\x18\x01 \x01(\tR\x04path\x12\x33\n\x06method\x18\x02 \x01(\x0e\x32\x1b.codefly.base.v0.HTTPMethodR\x06method\"\xa8\x01\n\x07RestAPI\x12\x18\n\x07service\x18\x01 \x01(\tR\x07service\x12\x16\n\x06module\x18\x02 \x01(\tR\x06module\x12\x37\n\x06groups\x18\x03 \x03(\x0b\x32\x1f.codefly.base.v0.RestRouteGroupR\x06groups\x12\x18\n\x07openapi\x18\x04 \x01(\x0cR\x07openapi\x12\x18\n\x07secured\x18\x05 \x01(\x08R\x07secured\"<\n\x03RPC\x12!\n\x0cservice_name\x18\x01 \x01(\tR\x0bserviceName\x12\x12\n\x04name\x18\x02 \x01(\tR\x04name\"\xaf\x01\n\x07GrpcAPI\x12\x18\n\x07service\x18\x01 \x01(\tR\x07service\x12\x16\n\x06module\x18\x02 \x01(\tR\x06module\x12\x18\n\x07package\x18\x03 \x01(\tR\x07package\x12(\n\x04rpcs\x18\x04 \x03(\x0b\x32\x14.codefly.base.v0.RPCR\x04rpcs\x12\x14\n\x05proto\x18\x05 \x01(\x0cR\x05proto\x12\x18\n\x07secured\x18\x06 \x01(\x08R\x07secured\"#\n\x07HttpAPI\x12\x18\n\x07secured\x18\x01 \x01(\x08R\x07secured\"\x08\n\x06TcpAPI*n\n\nHTTPMethod\x12\x07\n\x03GET\x10\x00\x12\x08\n\x04POST\x10\x01\x12\x07\n\x03PUT\x10\x02\x12\n\n\x06\x44\x45LETE\x10\x03\x12\t\n\x05PATCH\x10\x04\x12\x0b\n\x07OPTIONS\x10\x05\x12\x08\n\x04HEAD\x10\x06\x12\x0b\n\x07\x43ONNECT\x10\x07\x12\t\n\x05TRACE\x10\x08\x42\xbc\x01\n\x13\x63om.codefly.base.v0B\rEndpointProtoP\x01Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\xa2\x02\x03\x43\x42V\xaa\x02\x0f\x43odefly.Base.V0\xca\x02\x0f\x43odefly\\Base\\V0\xe2\x02\x1b\x43odefly\\Base\\V0\\GPBMetadata\xea\x02\x11\x43odefly::Base::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'codefly.base.v0.endpoint_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\023com.codefly.base.v0B\rEndpointProtoP\001Z8github.com/codefly-dev/core/generated/go/codefly/base/v0\242\002\003CBV\252\002\017Codefly.Base.V0\312\002\017Codefly\\Base\\V0\342\002\033Codefly\\Base\\V0\\GPBMetadata\352\002\021Codefly::Base::V0'
  _globals['_ENDPOINT'].fields_by_name['name']._loaded_options = None
  _globals['_ENDPOINT'].fields_by_name['name']._serialized_options = b'\272H\022r\020\020\003\030\0242\010^[a-z]+$h\001'
  _globals['_ENDPOINT'].fields_by_name['service']._loaded_options = None
  _globals['_ENDPOINT'].fields_by_name['service']._serialized_options = b'\272H\033r\031\020\003\030\0312\014^[a-z0-9-]+$h\001\272\001\002--'
  _globals['_ENDPOINT'].fields_by_name['module']._loaded_options = None
  _globals['_ENDPOINT'].fields_by_name['module']._serialized_options = b'\272H\033r\031\020\003\030\0312\014^[a-z0-9-]+$h\001\272\001\002--'
  _globals['_ENDPOINT'].fields_by_name['visibility']._loaded_options = None
  _globals['_ENDPOINT'].fields_by_name['visibility']._serialized_options = b'\272H%r#R\010externalR\006publicR\006moduleR\007private'
  _globals['_ENDPOINT'].fields_by_name['api']._loaded_options = None
  _globals['_ENDPOINT'].fields_by_name['api']._serialized_options = b'\272H\031r\027R\004httpR\004grpcR\003tcpR\004rest'
  _globals['_HTTPMETHOD']._serialized_start=1301
  _globals['_HTTPMETHOD']._serialized_end=1411
  _globals['_ENDPOINT']._serialized_start=81
  _globals['_ENDPOINT']._serialized_end=459
  _globals['_API']._serialized_start=462
  _globals['_API']._serialized_end=665
  _globals['_RESTROUTEGROUP']._serialized_start=667
  _globals['_RESTROUTEGROUP']._serialized_end=755
  _globals['_RESTROUTE']._serialized_start=757
  _globals['_RESTROUTE']._serialized_end=841
  _globals['_RESTAPI']._serialized_start=844
  _globals['_RESTAPI']._serialized_end=1012
  _globals['_RPC']._serialized_start=1014
  _globals['_RPC']._serialized_end=1074
  _globals['_GRPCAPI']._serialized_start=1077
  _globals['_GRPCAPI']._serialized_end=1252
  _globals['_HTTPAPI']._serialized_start=1254
  _globals['_HTTPAPI']._serialized_end=1289
  _globals['_TCPAPI']._serialized_start=1291
  _globals['_TCPAPI']._serialized_end=1299
# @@protoc_insertion_point(module_scope)
