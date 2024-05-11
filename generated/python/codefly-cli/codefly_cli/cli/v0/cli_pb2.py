# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: cli/v0/cli.proto
# Protobuf Python Version: 5.26.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from base.v0 import endpoint_pb2 as base_dot_v0_dot_endpoint__pb2
from base.v0 import workspace_pb2 as base_dot_v0_dot_workspace__pb2
from base.v0 import configuration_pb2 as base_dot_v0_dot_configuration__pb2
from services.agent.v0 import agent_pb2 as services_dot_agent_dot_v0_dot_agent__pb2
from services.runtime.v0 import runtime_pb2 as services_dot_runtime_dot_v0_dot_runtime__pb2
from observability.v0 import inventory_pb2 as observability_dot_v0_dot_inventory__pb2
from observability.v0 import dependencies_pb2 as observability_dot_v0_dot_dependencies__pb2
from observability.v0 import logs_pb2 as observability_dot_v0_dot_logs__pb2
from google.protobuf import empty_pb2 as google_dot_protobuf_dot_empty__pb2
from google.api import annotations_pb2 as google_dot_api_dot_annotations__pb2
from google.protobuf import timestamp_pb2 as google_dot_protobuf_dot_timestamp__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n\x10\x63li/v0/cli.proto\x12\x10observability.v0\x1a\x16\x62\x61se/v0/endpoint.proto\x1a\x17\x62\x61se/v0/workspace.proto\x1a\x1b\x62\x61se/v0/configuration.proto\x1a\x1dservices/agent/v0/agent.proto\x1a!services/runtime/v0/runtime.proto\x1a observability/v0/inventory.proto\x1a#observability/v0/dependencies.proto\x1a\x1bobservability/v0/logs.proto\x1a\x1bgoogle/protobuf/empty.proto\x1a\x1cgoogle/api/annotations.proto\x1a\x1fgoogle/protobuf/timestamp.proto\"2\n\x1aGetAgentInformationRequest\x12\x14\n\x05\x61gent\x18\x01 \x01(\tR\x05\x61gent\"M\n\x12MultiGraphResponse\x12\x37\n\x06graphs\x18\x01 \x03(\x0b\x32\x1f.observability.v0.GraphResponseR\x06graphs\"`\n\x0e\x41\x63tiveResponse\x12\x1c\n\tworkspace\x18\x01 \x01(\tR\tworkspace\x12\x16\n\x06module\x18\x02 \x01(\tR\x06module\x12\x18\n\x07service\x18\x03 \x01(\tR\x07service\"c\n\x12RunningInformation\x12\x16\n\x06module\x18\x01 \x01(\tR\x06module\x12\x18\n\x07service\x18\x02 \x01(\tR\x07service\x12\x1b\n\tagent_pid\x18\x03 \x01(\x05R\x08\x61gentPid\"a\n\x11GetAddressRequest\x12\x16\n\x06module\x18\x01 \x01(\tR\x06module\x12\x18\n\x07service\x18\x02 \x01(\tR\x07service\x12\x1a\n\x08\x65ndpoint\x18\x03 \x01(\tR\x08\x65ndpoint\".\n\x12GetAddressResponse\x12\x18\n\x07\x61\x64\x64ress\x18\x01 \x01(\tR\x07\x61\x64\x64ress\"K\n\x17GetConfigurationRequest\x12\x16\n\x06module\x18\x01 \x01(\tR\x06module\x12\x18\n\x07service\x18\x02 \x01(\tR\x07service\"X\n\x18GetConfigurationResponse\x12<\n\rconfiguration\x18\x01 \x01(\x0b\x32\x16.base.v0.ConfigurationR\rconfiguration\"[\n\x19GetConfigurationsResponse\x12>\n\x0e\x63onfigurations\x18\x01 \x03(\x0b\x32\x16.base.v0.ConfigurationR\x0e\x63onfigurations\"\"\n\nFlowStatus\x12\x14\n\x05ready\x18\x01 \x01(\x08R\x05ready\"\x11\n\x0fStopFlowRequest\"\x12\n\x10StopFlowResponse\"\x14\n\x12\x44\x65stroyFlowRequest\"\x15\n\x13\x44\x65stroyFlowResponse2\x8e\x0f\n\x03\x43LI\x12\x45\n\x04Ping\x12\x16.google.protobuf.Empty\x1a\x16.google.protobuf.Empty\"\r\x82\xd3\xe4\x93\x02\x07\x12\x05/ping\x12\x8c\x01\n\x13GetAgentInformation\x12,.observability.v0.GetAgentInformationRequest\x1a#.services.agent.v0.AgentInformation\"\"\x82\xd3\xe4\x93\x02\x1c\x12\x1a/agent/{agent}/information\x12\x61\n\x15GetWorkspaceInventory\x12\x16.google.protobuf.Empty\x1a\x12.base.v0.Workspace\"\x1c\x82\xd3\xe4\x93\x02\x16\x12\x14/workspace/inventory\x12\x8a\x01\n\"GetWorkspaceServiceDependencyGraph\x12\x16.google.protobuf.Empty\x1a\x1f.observability.v0.GraphResponse\"+\x82\xd3\xe4\x93\x02%\x12#/workspace/service-dependency-graph\x12\x91\x01\n(GetWorkspacePublicModulesDependencyGraph\x12\x16.google.protobuf.Empty\x1a$.observability.v0.MultiGraphResponse\"\'\x82\xd3\xe4\x93\x02!\x12\x1f/workspace/public-modules-graph\x12\x65\n\tGetActive\x12\x16.google.protobuf.Empty\x1a .observability.v0.ActiveResponse\"\x1e\x82\xd3\xe4\x93\x02\x18\x12\x16/workspace/information\x12\x9b\x01\n\x0cGetAddresses\x12#.observability.v0.GetAddressRequest\x1a$.observability.v0.GetAddressResponse\"@\x82\xd3\xe4\x93\x02:\x12\x38/workspace/network-mapping/{module}/{service}/{endpoint}\x12\x9e\x01\n\x10GetConfiguration\x12).observability.v0.GetConfigurationRequest\x1a*.observability.v0.GetConfigurationResponse\"3\x82\xd3\xe4\x93\x02-\x12+/workspace/configuration/{module}/{service}\x12\xba\x01\n\x1dGetDependenciesConfigurations\x12).observability.v0.GetConfigurationRequest\x1a+.observability.v0.GetConfigurationsResponse\"A\x82\xd3\xe4\x93\x02;\x12\x39/workspace/dependencies-configurations/{module}/{service}\x12\xb0\x01\n\x18GetRuntimeConfigurations\x12).observability.v0.GetConfigurationRequest\x1a+.observability.v0.GetConfigurationsResponse\"<\x82\xd3\xe4\x93\x02\x36\x12\x34/workspace/runtime-configurations/{module}/{service}\x12P\n\x04Logs\x12\x16.google.protobuf.Empty\x1a\x15.observability.v0.Log\"\x17\x82\xd3\xe4\x93\x02\x11\x12\x0f/workspace/logs0\x01\x12p\n\x10\x41\x63tiveLogHistory\x12\x1c.observability.v0.LogRequest\x1a\x1d.observability.v0.LogResponse\"\x1f\x82\xd3\xe4\x93\x02\x19\x12\x17/workspace/logs/history\x12\x65\n\rGetFlowStatus\x12\x16.google.protobuf.Empty\x1a\x1c.observability.v0.FlowStatus\"\x1e\x82\xd3\xe4\x93\x02\x18\x12\x16/workspace/flow/status\x12o\n\x08StopFlow\x12!.observability.v0.StopFlowRequest\x1a\".observability.v0.StopFlowResponse\"\x1c\x82\xd3\xe4\x93\x02\x16\"\x14/workspace/flow/stop\x12{\n\x0b\x44\x65stroyFlow\x12$.observability.v0.DestroyFlowRequest\x1a%.observability.v0.DestroyFlowResponse\"\x1f\x82\xd3\xe4\x93\x02\x19\"\x17/workspace/flow/destroyB\xb2\x01\n\x14\x63om.observability.v0B\x08\x43liProtoP\x01Z/github.com/codefly-dev/core/generated/go/cli/v0\xa2\x02\x03OVX\xaa\x02\x10Observability.V0\xca\x02\x10Observability\\V0\xe2\x02\x1cObservability\\V0\\GPBMetadata\xea\x02\x11Observability::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'cli.v0.cli_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\024com.observability.v0B\010CliProtoP\001Z/github.com/codefly-dev/core/generated/go/cli/v0\242\002\003OVX\252\002\020Observability.V0\312\002\020Observability\\V0\342\002\034Observability\\V0\\GPBMetadata\352\002\021Observability::V0'
  _globals['_CLI'].methods_by_name['Ping']._loaded_options = None
  _globals['_CLI'].methods_by_name['Ping']._serialized_options = b'\202\323\344\223\002\007\022\005/ping'
  _globals['_CLI'].methods_by_name['GetAgentInformation']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetAgentInformation']._serialized_options = b'\202\323\344\223\002\034\022\032/agent/{agent}/information'
  _globals['_CLI'].methods_by_name['GetWorkspaceInventory']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetWorkspaceInventory']._serialized_options = b'\202\323\344\223\002\026\022\024/workspace/inventory'
  _globals['_CLI'].methods_by_name['GetWorkspaceServiceDependencyGraph']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetWorkspaceServiceDependencyGraph']._serialized_options = b'\202\323\344\223\002%\022#/workspace/service-dependency-graph'
  _globals['_CLI'].methods_by_name['GetWorkspacePublicModulesDependencyGraph']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetWorkspacePublicModulesDependencyGraph']._serialized_options = b'\202\323\344\223\002!\022\037/workspace/public-modules-graph'
  _globals['_CLI'].methods_by_name['GetActive']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetActive']._serialized_options = b'\202\323\344\223\002\030\022\026/workspace/information'
  _globals['_CLI'].methods_by_name['GetAddresses']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetAddresses']._serialized_options = b'\202\323\344\223\002:\0228/workspace/network-mapping/{module}/{service}/{endpoint}'
  _globals['_CLI'].methods_by_name['GetConfiguration']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetConfiguration']._serialized_options = b'\202\323\344\223\002-\022+/workspace/configuration/{module}/{service}'
  _globals['_CLI'].methods_by_name['GetDependenciesConfigurations']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetDependenciesConfigurations']._serialized_options = b'\202\323\344\223\002;\0229/workspace/dependencies-configurations/{module}/{service}'
  _globals['_CLI'].methods_by_name['GetRuntimeConfigurations']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetRuntimeConfigurations']._serialized_options = b'\202\323\344\223\0026\0224/workspace/runtime-configurations/{module}/{service}'
  _globals['_CLI'].methods_by_name['Logs']._loaded_options = None
  _globals['_CLI'].methods_by_name['Logs']._serialized_options = b'\202\323\344\223\002\021\022\017/workspace/logs'
  _globals['_CLI'].methods_by_name['ActiveLogHistory']._loaded_options = None
  _globals['_CLI'].methods_by_name['ActiveLogHistory']._serialized_options = b'\202\323\344\223\002\031\022\027/workspace/logs/history'
  _globals['_CLI'].methods_by_name['GetFlowStatus']._loaded_options = None
  _globals['_CLI'].methods_by_name['GetFlowStatus']._serialized_options = b'\202\323\344\223\002\030\022\026/workspace/flow/status'
  _globals['_CLI'].methods_by_name['StopFlow']._loaded_options = None
  _globals['_CLI'].methods_by_name['StopFlow']._serialized_options = b'\202\323\344\223\002\026\"\024/workspace/flow/stop'
  _globals['_CLI'].methods_by_name['DestroyFlow']._loaded_options = None
  _globals['_CLI'].methods_by_name['DestroyFlow']._serialized_options = b'\202\323\344\223\002\031\"\027/workspace/flow/destroy'
  _globals['_GETAGENTINFORMATIONREQUEST']._serialized_start=374
  _globals['_GETAGENTINFORMATIONREQUEST']._serialized_end=424
  _globals['_MULTIGRAPHRESPONSE']._serialized_start=426
  _globals['_MULTIGRAPHRESPONSE']._serialized_end=503
  _globals['_ACTIVERESPONSE']._serialized_start=505
  _globals['_ACTIVERESPONSE']._serialized_end=601
  _globals['_RUNNINGINFORMATION']._serialized_start=603
  _globals['_RUNNINGINFORMATION']._serialized_end=702
  _globals['_GETADDRESSREQUEST']._serialized_start=704
  _globals['_GETADDRESSREQUEST']._serialized_end=801
  _globals['_GETADDRESSRESPONSE']._serialized_start=803
  _globals['_GETADDRESSRESPONSE']._serialized_end=849
  _globals['_GETCONFIGURATIONREQUEST']._serialized_start=851
  _globals['_GETCONFIGURATIONREQUEST']._serialized_end=926
  _globals['_GETCONFIGURATIONRESPONSE']._serialized_start=928
  _globals['_GETCONFIGURATIONRESPONSE']._serialized_end=1016
  _globals['_GETCONFIGURATIONSRESPONSE']._serialized_start=1018
  _globals['_GETCONFIGURATIONSRESPONSE']._serialized_end=1109
  _globals['_FLOWSTATUS']._serialized_start=1111
  _globals['_FLOWSTATUS']._serialized_end=1145
  _globals['_STOPFLOWREQUEST']._serialized_start=1147
  _globals['_STOPFLOWREQUEST']._serialized_end=1164
  _globals['_STOPFLOWRESPONSE']._serialized_start=1166
  _globals['_STOPFLOWRESPONSE']._serialized_end=1184
  _globals['_DESTROYFLOWREQUEST']._serialized_start=1186
  _globals['_DESTROYFLOWREQUEST']._serialized_end=1206
  _globals['_DESTROYFLOWRESPONSE']._serialized_start=1208
  _globals['_DESTROYFLOWRESPONSE']._serialized_end=1229
  _globals['_CLI']._serialized_start=1232
  _globals['_CLI']._serialized_end=3166
# @@protoc_insertion_point(module_scope)