# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: services/runtime/runtime.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from base import api_pb2 as base_dot_api__pb2
from agents import communicate_pb2 as agents_dot_communicate__pb2
from services import init_pb2 as services_dot_init__pb2
from services.runtime import tracker_pb2 as services_dot_runtime_dot_tracker__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x1eservices/runtime/runtime.proto\x12\x13v1.services.runtime\x1a\x0e\x62\x61se/api.proto\x1a\x18\x61gents/communicate.proto\x1a\x13services/init.proto\x1a\x1eservices/runtime/tracker.proto\"\x8f\x01\n\nInitStatus\x12;\n\x05state\x18\x01 \x01(\x0e\x32%.v1.services.runtime.InitStatus.StateR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"*\n\x05State\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05READY\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\xe4\x01\n\x0cInitResponse\x12.\n\x07version\x18\x01 \x01(\x0b\x32\x14.v1.services.VersionR\x07version\x12:\n\x08\x63hannels\x18\x02 \x03(\x0b\x32\x1e.v1.agents.communicate.ChannelR\x08\x63hannels\x12/\n\tendpoints\x18\x03 \x03(\x0b\x32\x11.v1.base.EndpointR\tendpoints\x12\x37\n\x06status\x18\x04 \x01(\x0b\x32\x1f.v1.services.runtime.InitStatusR\x06status\"\x99\x01\n\x0f\x43onfigureStatus\x12@\n\x05state\x18\x01 \x01(\x0e\x32*.v1.services.runtime.ConfigureStatus.StateR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"*\n\x05State\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05READY\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\x12\n\x10\x43onfigureRequest\"\xa1\x01\n\x11\x43onfigureResponse\x12<\n\x06status\x18\x02 \x01(\x0b\x32$.v1.services.runtime.ConfigureStatusR\x06status\x12N\n\x10network_mappings\x18\x03 \x03(\x0b\x32#.v1.services.runtime.NetworkMappingR\x0fnetworkMappings\"\x99\x01\n\x0eNetworkMapping\x12 \n\x0b\x61pplication\x18\x01 \x01(\tR\x0b\x61pplication\x12\x18\n\x07service\x18\x02 \x01(\tR\x07service\x12-\n\x08\x65ndpoint\x18\x03 \x01(\x0b\x32\x11.v1.base.EndpointR\x08\x65ndpoint\x12\x1c\n\taddresses\x18\x04 \x03(\tR\taddresses\"\xb2\x01\n\x0cStartRequest\x12N\n\x10network_mappings\x18\x01 \x03(\x0b\x32#.v1.services.runtime.NetworkMappingR\x0fnetworkMappings\x12R\n\x19\x64\x65pendency_endpoint_group\x18\x02 \x01(\x0b\x32\x16.v1.base.EndpointGroupR\x17\x64\x65pendencyEndpointGroup\"\x93\x01\n\x0bStartStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.v1.services.runtime.StartStatus.StateR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\",\n\x05State\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07STARTED\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\x83\x01\n\rStartResponse\x12\x38\n\x06status\x18\x01 \x01(\x0b\x32 .v1.services.runtime.StartStatusR\x06status\x12\x38\n\x08trackers\x18\x02 \x03(\x0b\x32\x1c.v1.services.runtime.TrackerR\x08trackers\"\x14\n\x12InformationRequest\"\xc9\x01\n\x13InformationResponse\x12G\n\x06status\x18\x01 \x01(\x0e\x32/.v1.services.runtime.InformationResponse.StatusR\x06status\"i\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x08\n\x04INIT\x10\x01\x12\x0b\n\x07STARTED\x10\x02\x12\x12\n\x0eRESTART_WANTED\x10\x03\x12\x0f\n\x0bSYNC_WANTED\x10\x04\x12\x0b\n\x07STOPPED\x10\x05\x12\t\n\x05\x45RROR\x10\x06\"\'\n\x0bStopRequest\x12\x18\n\x07persist\x18\x01 \x01(\x08R\x07persist\"\x0e\n\x0cStopResponse2\x8e\x04\n\x07Runtime\x12\x45\n\x04Init\x12\x18.v1.services.InitRequest\x1a!.v1.services.runtime.InitResponse\"\x00\x12\\\n\tConfigure\x12%.v1.services.runtime.ConfigureRequest\x1a&.v1.services.runtime.ConfigureResponse\"\x00\x12P\n\x05Start\x12!.v1.services.runtime.StartRequest\x1a\".v1.services.runtime.StartResponse\"\x00\x12\x62\n\x0bInformation\x12\'.v1.services.runtime.InformationRequest\x1a(.v1.services.runtime.InformationResponse\"\x00\x12M\n\x04Stop\x12 .v1.services.runtime.StopRequest\x1a!.v1.services.runtime.StopResponse\"\x00\x12Y\n\x0b\x43ommunicate\x12\x1d.v1.agents.communicate.Engage\x1a).v1.agents.communicate.InformationRequest\"\x00\x42\xcf\x01\n\x17\x63om.v1.services.runtimeB\x0cRuntimeProtoP\x01Z8github.com/codefly-dev/core/proto/v1/go/services/runtime\xa2\x02\x03VSR\xaa\x02\x13V1.Services.Runtime\xca\x02\x13V1\\Services\\Runtime\xe2\x02\x1fV1\\Services\\Runtime\\GPBMetadata\xea\x02\x15V1::Services::Runtimeb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'services.runtime.runtime_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\027com.v1.services.runtimeB\014RuntimeProtoP\001Z8github.com/codefly-dev/core/proto/v1/go/services/runtime\242\002\003VSR\252\002\023V1.Services.Runtime\312\002\023V1\\Services\\Runtime\342\002\037V1\\Services\\Runtime\\GPBMetadata\352\002\025V1::Services::Runtime'
  _globals['_INITSTATUS']._serialized_start=151
  _globals['_INITSTATUS']._serialized_end=294
  _globals['_INITSTATUS_STATE']._serialized_start=252
  _globals['_INITSTATUS_STATE']._serialized_end=294
  _globals['_INITRESPONSE']._serialized_start=297
  _globals['_INITRESPONSE']._serialized_end=525
  _globals['_CONFIGURESTATUS']._serialized_start=528
  _globals['_CONFIGURESTATUS']._serialized_end=681
  _globals['_CONFIGURESTATUS_STATE']._serialized_start=252
  _globals['_CONFIGURESTATUS_STATE']._serialized_end=294
  _globals['_CONFIGUREREQUEST']._serialized_start=683
  _globals['_CONFIGUREREQUEST']._serialized_end=701
  _globals['_CONFIGURERESPONSE']._serialized_start=704
  _globals['_CONFIGURERESPONSE']._serialized_end=865
  _globals['_NETWORKMAPPING']._serialized_start=868
  _globals['_NETWORKMAPPING']._serialized_end=1021
  _globals['_STARTREQUEST']._serialized_start=1024
  _globals['_STARTREQUEST']._serialized_end=1202
  _globals['_STARTSTATUS']._serialized_start=1205
  _globals['_STARTSTATUS']._serialized_end=1352
  _globals['_STARTSTATUS_STATE']._serialized_start=1308
  _globals['_STARTSTATUS_STATE']._serialized_end=1352
  _globals['_STARTRESPONSE']._serialized_start=1355
  _globals['_STARTRESPONSE']._serialized_end=1486
  _globals['_INFORMATIONREQUEST']._serialized_start=1488
  _globals['_INFORMATIONREQUEST']._serialized_end=1508
  _globals['_INFORMATIONRESPONSE']._serialized_start=1511
  _globals['_INFORMATIONRESPONSE']._serialized_end=1712
  _globals['_INFORMATIONRESPONSE_STATUS']._serialized_start=1607
  _globals['_INFORMATIONRESPONSE_STATUS']._serialized_end=1712
  _globals['_STOPREQUEST']._serialized_start=1714
  _globals['_STOPREQUEST']._serialized_end=1753
  _globals['_STOPRESPONSE']._serialized_start=1755
  _globals['_STOPRESPONSE']._serialized_end=1769
  _globals['_RUNTIME']._serialized_start=1772
  _globals['_RUNTIME']._serialized_end=2298
# @@protoc_insertion_point(module_scope)
