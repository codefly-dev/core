# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: services/runtime/v0/runtime.proto
# Protobuf Python Version: 5.26.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from base.v0 import scope_pb2 as base_dot_v0_dot_scope__pb2
from base.v0 import spec_pb2 as base_dot_v0_dot_spec__pb2
from base.v0 import environment_pb2 as base_dot_v0_dot_environment__pb2
from base.v0 import service_pb2 as base_dot_v0_dot_service__pb2
from base.v0 import endpoint_pb2 as base_dot_v0_dot_endpoint__pb2
from base.v0 import network_pb2 as base_dot_v0_dot_network__pb2
from base.v0 import configuration_pb2 as base_dot_v0_dot_configuration__pb2
from services.agent.v0 import agent_pb2 as services_dot_agent_dot_v0_dot_agent__pb2
from services.agent.v0 import communicate_pb2 as services_dot_agent_dot_v0_dot_communicate__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n!services/runtime/v0/runtime.proto\x12\x13services.runtime.v0\x1a\x13\x62\x61se/v0/scope.proto\x1a\x12\x62\x61se/v0/spec.proto\x1a\x19\x62\x61se/v0/environment.proto\x1a\x15\x62\x61se/v0/service.proto\x1a\x16\x62\x61se/v0/endpoint.proto\x1a\x15\x62\x61se/v0/network.proto\x1a\x1b\x62\x61se/v0/configuration.proto\x1a\x1dservices/agent/v0/agent.proto\x1a#services/agent/v0/communicate.proto\"\x91\x01\n\nLoadStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.runtime.v0.LoadStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"+\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05READY\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\xc9\x01\n\x0bLoadRequest\x12\'\n\x0f\x64\x65veloper_debug\x18\x01 \x01(\x08R\x0e\x64\x65veloperDebug\x12#\n\rdisable_catch\x18\x02 \x01(\x08R\x0c\x64isableCatch\x12\x34\n\x08identity\x18\x03 \x01(\x0b\x32\x18.base.v0.ServiceIdentityR\x08identity\x12\x36\n\x0b\x65nvironment\x18\x04 \x01(\x0b\x32\x14.base.v0.EnvironmentR\x0b\x65nvironment\"\xa4\x01\n\x0cLoadResponse\x12*\n\x07version\x18\x01 \x01(\x0b\x32\x10.base.v0.VersionR\x07version\x12\x37\n\x06status\x18\x02 \x01(\x0b\x32\x1f.services.runtime.v0.LoadStatusR\x06status\x12/\n\tendpoints\x18\x03 \x03(\x0b\x32\x11.base.v0.EndpointR\tendpoints\"\x91\x01\n\nInitStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.runtime.v0.InitStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"+\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05READY\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\x85\x03\n\x0bInitRequest\x12@\n\x0fruntime_context\x18\x01 \x01(\x0b\x32\x17.base.v0.RuntimeContextR\x0eruntimeContext\x12<\n\rconfiguration\x18\x02 \x01(\x0b\x32\x16.base.v0.ConfigurationR\rconfiguration\x12S\n\x19proposed_network_mappings\x18\x03 \x03(\x0b\x32\x17.base.v0.NetworkMappingR\x17proposedNetworkMappings\x12H\n\x16\x64\x65pendencies_endpoints\x18\x04 \x03(\x0b\x32\x11.base.v0.EndpointR\x15\x64\x65pendenciesEndpoints\x12W\n\x1b\x64\x65pendencies_configurations\x18\x05 \x03(\x0b\x32\x16.base.v0.ConfigurationR\x1a\x64\x65pendenciesConfigurations\"\x9c\x02\n\x0cInitResponse\x12\x37\n\x06status\x18\x01 \x01(\x0b\x32\x1f.services.runtime.v0.InitStatusR\x06status\x12@\n\x0fruntime_context\x18\x02 \x01(\x0b\x32\x17.base.v0.RuntimeContextR\x0eruntimeContext\x12\x42\n\x10network_mappings\x18\x03 \x03(\x0b\x32\x17.base.v0.NetworkMappingR\x0fnetworkMappings\x12M\n\x16runtime_configurations\x18\x04 \x03(\x0b\x32\x16.base.v0.ConfigurationR\x15runtimeConfigurations\"\x91\x01\n\x0cStartRequest\x12$\n\x05specs\x18\x01 \x01(\x0b\x32\x0e.base.v0.SpecsR\x05specs\x12[\n\x1d\x64\x65pendencies_network_mappings\x18\x02 \x03(\x0b\x32\x17.base.v0.NetworkMappingR\x1b\x64\x65pendenciesNetworkMappings\"\x95\x01\n\x0bStartStatus\x12=\n\x05state\x18\x01 \x01(\x0e\x32\'.services.runtime.v0.StartStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07STARTED\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"I\n\rStartResponse\x12\x38\n\x06status\x18\x01 \x01(\x0b\x32 .services.runtime.v0.StartStatusR\x06status\"\x93\x01\n\nTestStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.runtime.v0.TestStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\r\n\x0bTestRequest\"G\n\x0cTestResponse\x12\x37\n\x06status\x18\x01 \x01(\x0b\x32\x1f.services.runtime.v0.TestStatusR\x06status\"\r\n\x0bStopRequest\"\x93\x01\n\nStopStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.runtime.v0.StopStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"G\n\x0cStopResponse\x12\x37\n\x06status\x18\x01 \x01(\x0b\x32\x1f.services.runtime.v0.StopStatusR\x06status\"\x10\n\x0e\x44\x65stroyRequest\"\x99\x01\n\rDestroyStatus\x12?\n\x05state\x18\x01 \x01(\x0e\x32).services.runtime.v0.DestroyStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"M\n\x0f\x44\x65stroyResponse\x12:\n\x06status\x18\x01 \x01(\x0b\x32\".services.runtime.v0.DestroyStatusR\x06status\"\x14\n\x12InformationRequest\"\x8c\x01\n\x0c\x44\x65siredState\x12=\n\x05stage\x18\x01 \x01(\x0e\x32\'.services.runtime.v0.DesiredState.StageR\x05stage\"=\n\x05Stage\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x08\n\x04NOOP\x10\x01\x12\x08\n\x04LOAD\x10\x02\x12\x08\n\x04INIT\x10\x03\x12\t\n\x05START\x10\x04\"\xf5\x03\n\x13InformationResponse\x12\x46\n\rdesired_state\x18\x01 \x01(\x0b\x32!.services.runtime.v0.DesiredStateR\x0c\x64\x65siredState\x12@\n\x0bload_status\x18\x02 \x01(\x0b\x32\x1f.services.runtime.v0.LoadStatusR\nloadStatus\x12@\n\x0binit_status\x18\x03 \x01(\x0b\x32\x1f.services.runtime.v0.InitStatusR\ninitStatus\x12\x43\n\x0cstart_status\x18\x04 \x01(\x0b\x32 .services.runtime.v0.StartStatusR\x0bstartStatus\x12@\n\x0bstop_status\x18\x05 \x01(\x0b\x32\x1f.services.runtime.v0.StopStatusR\nstopStatus\x12I\n\x0e\x44\x65stroy_status\x18\x06 \x01(\x0b\x32\".services.runtime.v0.DestroyStatusR\rDestroyStatus\x12@\n\x0btest_status\x18\x07 \x01(\x0b\x32\x1f.services.runtime.v0.TestStatusR\ntestStatus2\xa6\x05\n\x07Runtime\x12M\n\x04Load\x12 .services.runtime.v0.LoadRequest\x1a!.services.runtime.v0.LoadResponse\"\x00\x12M\n\x04Init\x12 .services.runtime.v0.InitRequest\x1a!.services.runtime.v0.InitResponse\"\x00\x12P\n\x05Start\x12!.services.runtime.v0.StartRequest\x1a\".services.runtime.v0.StartResponse\"\x00\x12M\n\x04Stop\x12 .services.runtime.v0.StopRequest\x1a!.services.runtime.v0.StopResponse\"\x00\x12V\n\x07\x44\x65stroy\x12#.services.runtime.v0.DestroyRequest\x1a$.services.runtime.v0.DestroyResponse\"\x00\x12M\n\x04Test\x12 .services.runtime.v0.TestRequest\x1a!.services.runtime.v0.TestResponse\"\x00\x12\x62\n\x0bInformation\x12\'.services.runtime.v0.InformationRequest\x1a(.services.runtime.v0.InformationResponse\"\x00\x12Q\n\x0b\x43ommunicate\x12\x19.services.agent.v0.Engage\x1a%.services.agent.v0.InformationRequest\"\x00\x42\xd3\x01\n\x17\x63om.services.runtime.v0B\x0cRuntimeProtoP\x01Z<github.com/codefly-dev/core/generated/go/services/runtime/v0\xa2\x02\x03SRV\xaa\x02\x13Services.Runtime.V0\xca\x02\x13Services\\Runtime\\V0\xe2\x02\x1fServices\\Runtime\\V0\\GPBMetadata\xea\x02\x15Services::Runtime::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'services.runtime.v0.runtime_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\027com.services.runtime.v0B\014RuntimeProtoP\001Z<github.com/codefly-dev/core/generated/go/services/runtime/v0\242\002\003SRV\252\002\023Services.Runtime.V0\312\002\023Services\\Runtime\\V0\342\002\037Services\\Runtime\\V0\\GPBMetadata\352\002\025Services::Runtime::V0'
  _globals['_LOADSTATUS']._serialized_start=294
  _globals['_LOADSTATUS']._serialized_end=439
  _globals['_LOADSTATUS_STATUS']._serialized_start=396
  _globals['_LOADSTATUS_STATUS']._serialized_end=439
  _globals['_LOADREQUEST']._serialized_start=442
  _globals['_LOADREQUEST']._serialized_end=643
  _globals['_LOADRESPONSE']._serialized_start=646
  _globals['_LOADRESPONSE']._serialized_end=810
  _globals['_INITSTATUS']._serialized_start=813
  _globals['_INITSTATUS']._serialized_end=958
  _globals['_INITSTATUS_STATUS']._serialized_start=396
  _globals['_INITSTATUS_STATUS']._serialized_end=439
  _globals['_INITREQUEST']._serialized_start=961
  _globals['_INITREQUEST']._serialized_end=1350
  _globals['_INITRESPONSE']._serialized_start=1353
  _globals['_INITRESPONSE']._serialized_end=1637
  _globals['_STARTREQUEST']._serialized_start=1640
  _globals['_STARTREQUEST']._serialized_end=1785
  _globals['_STARTSTATUS']._serialized_start=1788
  _globals['_STARTSTATUS']._serialized_end=1937
  _globals['_STARTSTATUS_STATUS']._serialized_start=1892
  _globals['_STARTSTATUS_STATUS']._serialized_end=1937
  _globals['_STARTRESPONSE']._serialized_start=1939
  _globals['_STARTRESPONSE']._serialized_end=2012
  _globals['_TESTSTATUS']._serialized_start=2015
  _globals['_TESTSTATUS']._serialized_end=2162
  _globals['_TESTSTATUS_STATUS']._serialized_start=2117
  _globals['_TESTSTATUS_STATUS']._serialized_end=2162
  _globals['_TESTREQUEST']._serialized_start=2164
  _globals['_TESTREQUEST']._serialized_end=2177
  _globals['_TESTRESPONSE']._serialized_start=2179
  _globals['_TESTRESPONSE']._serialized_end=2250
  _globals['_STOPREQUEST']._serialized_start=2252
  _globals['_STOPREQUEST']._serialized_end=2265
  _globals['_STOPSTATUS']._serialized_start=2268
  _globals['_STOPSTATUS']._serialized_end=2415
  _globals['_STOPSTATUS_STATUS']._serialized_start=2117
  _globals['_STOPSTATUS_STATUS']._serialized_end=2162
  _globals['_STOPRESPONSE']._serialized_start=2417
  _globals['_STOPRESPONSE']._serialized_end=2488
  _globals['_DESTROYREQUEST']._serialized_start=2490
  _globals['_DESTROYREQUEST']._serialized_end=2506
  _globals['_DESTROYSTATUS']._serialized_start=2509
  _globals['_DESTROYSTATUS']._serialized_end=2662
  _globals['_DESTROYSTATUS_STATUS']._serialized_start=2117
  _globals['_DESTROYSTATUS_STATUS']._serialized_end=2162
  _globals['_DESTROYRESPONSE']._serialized_start=2664
  _globals['_DESTROYRESPONSE']._serialized_end=2741
  _globals['_INFORMATIONREQUEST']._serialized_start=2743
  _globals['_INFORMATIONREQUEST']._serialized_end=2763
  _globals['_DESIREDSTATE']._serialized_start=2766
  _globals['_DESIREDSTATE']._serialized_end=2906
  _globals['_DESIREDSTATE_STAGE']._serialized_start=2845
  _globals['_DESIREDSTATE_STAGE']._serialized_end=2906
  _globals['_INFORMATIONRESPONSE']._serialized_start=2909
  _globals['_INFORMATIONRESPONSE']._serialized_end=3410
  _globals['_RUNTIME']._serialized_start=3413
  _globals['_RUNTIME']._serialized_end=4091
# @@protoc_insertion_point(module_scope)
