# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# NO CHECKED-IN PROTOBUF GENCODE
# source: services/builder/v0/builder.proto
# Protobuf Python Version: 5.27.0
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
    0,
    '',
    'services/builder/v0/builder.proto'
)
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()


from base.v0 import service_pb2 as base_dot_v0_dot_service__pb2
from base.v0 import network_pb2 as base_dot_v0_dot_network__pb2
from base.v0 import endpoint_pb2 as base_dot_v0_dot_endpoint__pb2
from base.v0 import environment_pb2 as base_dot_v0_dot_environment__pb2
from base.v0 import configuration_pb2 as base_dot_v0_dot_configuration__pb2
from services.agent.v0 import communicate_pb2 as services_dot_agent_dot_v0_dot_communicate__pb2
from services.builder.v0 import docker_pb2 as services_dot_builder_dot_v0_dot_docker__pb2
from services.builder.v0 import deployment_pb2 as services_dot_builder_dot_v0_dot_deployment__pb2


DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n!services/builder/v0/builder.proto\x12\x13services.builder.v0\x1a\x15\x62\x61se/v0/service.proto\x1a\x15\x62\x61se/v0/network.proto\x1a\x16\x62\x61se/v0/endpoint.proto\x1a\x19\x62\x61se/v0/environment.proto\x1a\x1b\x62\x61se/v0/configuration.proto\x1a#services/agent/v0/communicate.proto\x1a services/builder/v0/docker.proto\x1a$services/builder/v0/deployment.proto\"\x91\x01\n\nLoadStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.builder.v0.LoadStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"+\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\t\n\x05READY\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\x95\x02\n\x0bLoadRequest\x12\'\n\x0f\x64\x65veloper_debug\x18\x01 \x01(\x08R\x0e\x64\x65veloperDebug\x12#\n\rdisable_catch\x18\x02 \x01(\x08R\x0c\x64isableCatch\x12\x34\n\x08identity\x18\x03 \x01(\x0b\x32\x18.base.v0.ServiceIdentityR\x08identity\x12\x46\n\rcreation_mode\x18\x04 \x01(\x0b\x32!.services.builder.v0.CreationModeR\x0c\x63reationMode\x12:\n\tsync_mode\x18\x05 \x01(\x0b\x32\x1d.services.builder.v0.SyncModeR\x08syncMode\"0\n\x0c\x43reationMode\x12 \n\x0b\x63ommunicate\x18\x01 \x01(\x08R\x0b\x63ommunicate\",\n\x08SyncMode\x12 \n\x0b\x63ommunicate\x18\x01 \x01(\x08R\x0b\x63ommunicate\"\xcb\x01\n\x0cLoadResponse\x12\x35\n\x05state\x18\x01 \x01(\x0b\x32\x1f.services.builder.v0.LoadStatusR\x05state\x12*\n\x07version\x18\x02 \x01(\x0b\x32\x10.base.v0.VersionR\x07version\x12/\n\tendpoints\x18\x03 \x03(\x0b\x32\x11.base.v0.EndpointR\tendpoints\x12\'\n\x0fgetting_started\x18\x04 \x01(\tR\x0egettingStarted\"\x0f\n\rCreateRequest\"\x97\x01\n\x0c\x43reateStatus\x12>\n\x05state\x18\x01 \x01(\x0e\x32(.services.builder.v0.CreateStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07\x43REATED\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"z\n\x0e\x43reateResponse\x12\x37\n\x05state\x18\x01 \x01(\x0b\x32!.services.builder.v0.CreateStatusR\x05state\x12/\n\tendpoints\x18\x02 \x03(\x0b\x32\x11.base.v0.EndpointR\tendpoints\"\x93\x01\n\nInitStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.builder.v0.InitStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"W\n\x0bInitRequest\x12H\n\x16\x64\x65pendencies_endpoints\x18\x01 \x03(\x0b\x32\x11.base.v0.EndpointR\x15\x64\x65pendenciesEndpoints\"E\n\x0cInitResponse\x12\x35\n\x05state\x18\x01 \x01(\x0b\x32\x1f.services.builder.v0.InitStatusR\x05state\"\x97\x01\n\x0cUpdateStatus\x12>\n\x05state\x18\x01 \x01(\x0e\x32(.services.builder.v0.UpdateStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\x0f\n\rUpdateRequest\"I\n\x0eUpdateResponse\x12\x37\n\x05state\x18\x01 \x01(\x0b\x32!.services.builder.v0.UpdateStatusR\x05state\"\r\n\x0bSyncRequest\"\x93\x01\n\nSyncStatus\x12<\n\x05state\x18\x01 \x01(\x0e\x32&.services.builder.v0.SyncStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"E\n\x0cSyncResponse\x12\x35\n\x05state\x18\x01 \x01(\x0b\x32\x1f.services.builder.v0.SyncStatusR\x05state\"s\n\x0c\x42uildContext\x12[\n\x14\x64ocker_build_context\x18\x01 \x01(\x0b\x32\'.services.builder.v0.DockerBuildContextH\x00R\x12\x64ockerBuildContextB\x06\n\x04kind\"V\n\x0c\x42uildRequest\x12\x46\n\rbuild_context\x18\x01 \x01(\x0b\x32!.services.builder.v0.BuildContextR\x0c\x62uildContext\"o\n\x0b\x42uildResult\x12X\n\x13\x64ocker_build_result\x18\x01 \x01(\x0b\x32&.services.builder.v0.DockerBuildResultH\x00R\x11\x64ockerBuildResultB\x06\n\x04kind\"\x95\x01\n\x0b\x42uildStatus\x12=\n\x05state\x18\x01 \x01(\x0e\x32\'.services.builder.v0.BuildStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\x81\x01\n\rBuildResponse\x12\x36\n\x05state\x18\x01 \x01(\x0b\x32 .services.builder.v0.BuildStatusR\x05state\x12\x38\n\x06result\x18\x02 \x01(\x0b\x32 .services.builder.v0.BuildResultR\x06result\"\xc4\x03\n\x11\x44\x65ploymentRequest\x12\x36\n\x0b\x65nvironment\x18\x01 \x01(\x0b\x32\x14.base.v0.EnvironmentR\x0b\x65nvironment\x12?\n\ndeployment\x18\x02 \x01(\x0b\x32\x1f.services.builder.v0.DeploymentR\ndeployment\x12<\n\rconfiguration\x18\x03 \x01(\x0b\x32\x16.base.v0.ConfigurationR\rconfiguration\x12W\n\x1b\x64\x65pendencies_configurations\x18\x04 \x03(\x0b\x32\x16.base.v0.ConfigurationR\x1a\x64\x65pendenciesConfigurations\x12\x42\n\x10network_mappings\x18\x05 \x03(\x0b\x32\x17.base.v0.NetworkMappingR\x0fnetworkMappings\x12[\n\x1d\x64\x65pendencies_network_mappings\x18\x06 \x03(\x0b\x32\x17.base.v0.NetworkMappingR\x1b\x64\x65pendenciesNetworkMappings\"\x9f\x01\n\x10\x44\x65ploymentStatus\x12\x42\n\x05state\x18\x01 \x01(\x0e\x32,.services.builder.v0.DeploymentStatus.StatusR\x05state\x12\x18\n\x07message\x18\x02 \x01(\tR\x07message\"-\n\x06Status\x12\x0b\n\x07UNKNOWN\x10\x00\x12\x0b\n\x07SUCCESS\x10\x01\x12\t\n\x05\x45RROR\x10\x02\"\xd6\x01\n\x12\x44\x65ploymentResponse\x12;\n\x05state\x18\x01 \x01(\x0b\x32%.services.builder.v0.DeploymentStatusR\x05state\x12<\n\rconfiguration\x18\x02 \x01(\x0b\x32\x16.base.v0.ConfigurationR\rconfiguration\x12\x45\n\ndeployment\x18\x03 \x01(\x0b\x32%.services.builder.v0.DeploymentOutputR\ndeployment2\xa2\x05\n\x07\x42uilder\x12M\n\x04Load\x12 .services.builder.v0.LoadRequest\x1a!.services.builder.v0.LoadResponse\"\x00\x12M\n\x04Init\x12 .services.builder.v0.InitRequest\x1a!.services.builder.v0.InitResponse\"\x00\x12S\n\x06\x43reate\x12\".services.builder.v0.CreateRequest\x1a#.services.builder.v0.CreateResponse\"\x00\x12S\n\x06Update\x12\".services.builder.v0.UpdateRequest\x1a#.services.builder.v0.UpdateResponse\"\x00\x12M\n\x04Sync\x12 .services.builder.v0.SyncRequest\x1a!.services.builder.v0.SyncResponse\"\x00\x12P\n\x05\x42uild\x12!.services.builder.v0.BuildRequest\x1a\".services.builder.v0.BuildResponse\"\x00\x12[\n\x06\x44\x65ploy\x12&.services.builder.v0.DeploymentRequest\x1a\'.services.builder.v0.DeploymentResponse\"\x00\x12Q\n\x0b\x43ommunicate\x12\x19.services.agent.v0.Engage\x1a%.services.agent.v0.InformationRequest\"\x00\x42\xd3\x01\n\x17\x63om.services.builder.v0B\x0c\x42uilderProtoP\x01Z<github.com/codefly-dev/core/generated/go/services/builder/v0\xa2\x02\x03SBV\xaa\x02\x13Services.Builder.V0\xca\x02\x13Services\\Builder\\V0\xe2\x02\x1fServices\\Builder\\V0\\GPBMetadata\xea\x02\x15Services::Builder::V0b\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'services.builder.v0.builder_pb2', _globals)
if not _descriptor._USE_C_DESCRIPTORS:
  _globals['DESCRIPTOR']._loaded_options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\027com.services.builder.v0B\014BuilderProtoP\001Z<github.com/codefly-dev/core/generated/go/services/builder/v0\242\002\003SBV\252\002\023Services.Builder.V0\312\002\023Services\\Builder\\V0\342\002\037Services\\Builder\\V0\\GPBMetadata\352\002\025Services::Builder::V0'
  _globals['_LOADSTATUS']._serialized_start=294
  _globals['_LOADSTATUS']._serialized_end=439
  _globals['_LOADSTATUS_STATUS']._serialized_start=396
  _globals['_LOADSTATUS_STATUS']._serialized_end=439
  _globals['_LOADREQUEST']._serialized_start=442
  _globals['_LOADREQUEST']._serialized_end=719
  _globals['_CREATIONMODE']._serialized_start=721
  _globals['_CREATIONMODE']._serialized_end=769
  _globals['_SYNCMODE']._serialized_start=771
  _globals['_SYNCMODE']._serialized_end=815
  _globals['_LOADRESPONSE']._serialized_start=818
  _globals['_LOADRESPONSE']._serialized_end=1021
  _globals['_CREATEREQUEST']._serialized_start=1023
  _globals['_CREATEREQUEST']._serialized_end=1038
  _globals['_CREATESTATUS']._serialized_start=1041
  _globals['_CREATESTATUS']._serialized_end=1192
  _globals['_CREATESTATUS_STATUS']._serialized_start=1147
  _globals['_CREATESTATUS_STATUS']._serialized_end=1192
  _globals['_CREATERESPONSE']._serialized_start=1194
  _globals['_CREATERESPONSE']._serialized_end=1316
  _globals['_INITSTATUS']._serialized_start=1319
  _globals['_INITSTATUS']._serialized_end=1466
  _globals['_INITSTATUS_STATUS']._serialized_start=1421
  _globals['_INITSTATUS_STATUS']._serialized_end=1466
  _globals['_INITREQUEST']._serialized_start=1468
  _globals['_INITREQUEST']._serialized_end=1555
  _globals['_INITRESPONSE']._serialized_start=1557
  _globals['_INITRESPONSE']._serialized_end=1626
  _globals['_UPDATESTATUS']._serialized_start=1629
  _globals['_UPDATESTATUS']._serialized_end=1780
  _globals['_UPDATESTATUS_STATUS']._serialized_start=1421
  _globals['_UPDATESTATUS_STATUS']._serialized_end=1466
  _globals['_UPDATEREQUEST']._serialized_start=1782
  _globals['_UPDATEREQUEST']._serialized_end=1797
  _globals['_UPDATERESPONSE']._serialized_start=1799
  _globals['_UPDATERESPONSE']._serialized_end=1872
  _globals['_SYNCREQUEST']._serialized_start=1874
  _globals['_SYNCREQUEST']._serialized_end=1887
  _globals['_SYNCSTATUS']._serialized_start=1890
  _globals['_SYNCSTATUS']._serialized_end=2037
  _globals['_SYNCSTATUS_STATUS']._serialized_start=1421
  _globals['_SYNCSTATUS_STATUS']._serialized_end=1466
  _globals['_SYNCRESPONSE']._serialized_start=2039
  _globals['_SYNCRESPONSE']._serialized_end=2108
  _globals['_BUILDCONTEXT']._serialized_start=2110
  _globals['_BUILDCONTEXT']._serialized_end=2225
  _globals['_BUILDREQUEST']._serialized_start=2227
  _globals['_BUILDREQUEST']._serialized_end=2313
  _globals['_BUILDRESULT']._serialized_start=2315
  _globals['_BUILDRESULT']._serialized_end=2426
  _globals['_BUILDSTATUS']._serialized_start=2429
  _globals['_BUILDSTATUS']._serialized_end=2578
  _globals['_BUILDSTATUS_STATUS']._serialized_start=1421
  _globals['_BUILDSTATUS_STATUS']._serialized_end=1466
  _globals['_BUILDRESPONSE']._serialized_start=2581
  _globals['_BUILDRESPONSE']._serialized_end=2710
  _globals['_DEPLOYMENTREQUEST']._serialized_start=2713
  _globals['_DEPLOYMENTREQUEST']._serialized_end=3165
  _globals['_DEPLOYMENTSTATUS']._serialized_start=3168
  _globals['_DEPLOYMENTSTATUS']._serialized_end=3327
  _globals['_DEPLOYMENTSTATUS_STATUS']._serialized_start=1421
  _globals['_DEPLOYMENTSTATUS_STATUS']._serialized_end=1466
  _globals['_DEPLOYMENTRESPONSE']._serialized_start=3330
  _globals['_DEPLOYMENTRESPONSE']._serialized_end=3544
  _globals['_BUILDER']._serialized_start=3547
  _globals['_BUILDER']._serialized_end=4221
# @@protoc_insertion_point(module_scope)
