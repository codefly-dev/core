# -*- coding: utf-8 -*-
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: proto/services/runtime/tracker.proto
# Protobuf Python Version: 4.25.1
"""Generated protocol buffer code."""
from google.protobuf import descriptor as _descriptor
from google.protobuf import descriptor_pool as _descriptor_pool
from google.protobuf import symbol_database as _symbol_database
from google.protobuf.internal import builder as _builder
# @@protoc_insertion_point(imports)

_sym_db = _symbol_database.Default()




DESCRIPTOR = _descriptor_pool.Default().AddSerializedFile(b'\n$proto/services/runtime/tracker.proto\x12\x13v1.services.runtime\"6\n\x0eProcessTracker\x12\x10\n\x03PID\x18\x01 \x01(\x05R\x03PID\x12\x12\n\x04type\x18\x02 \x01(\tR\x04type\"2\n\rDockerTracker\x12!\n\x0c\x63ontainer_id\x18\x01 \x01(\tR\x0b\x63ontainerId\"\xc5\x01\n\x07Tracker\x12\x12\n\x04name\x18\x01 \x01(\tR\x04name\x12N\n\x0fprocess_tracker\x18\x04 \x01(\x0b\x32#.v1.services.runtime.ProcessTrackerH\x00R\x0eprocessTracker\x12K\n\x0e\x64ocker_tracker\x18\x05 \x01(\x0b\x32\".v1.services.runtime.DockerTrackerH\x00R\rdockerTrackerB\t\n\x07tracker\"G\n\x0bTrackerList\x12\x38\n\x08trackers\x18\x01 \x03(\x0b\x32\x1c.v1.services.runtime.TrackerR\x08trackers\"\xb2\x01\n\x08Trackers\x12G\n\x08trackers\x18\x01 \x03(\x0b\x32+.v1.services.runtime.Trackers.TrackersEntryR\x08trackers\x1a]\n\rTrackersEntry\x12\x10\n\x03key\x18\x01 \x01(\tR\x03key\x12\x36\n\x05value\x18\x02 \x01(\x0b\x32 .v1.services.runtime.TrackerListR\x05value:\x02\x38\x01\x42\xd9\x01\n\x17\x63om.v1.services.runtimeB\x0cTrackerProtoP\x01ZBgithub.com/codefly-dev/core/generated/v1/go/proto/services/runtime\xa2\x02\x03VSR\xaa\x02\x13V1.Services.Runtime\xca\x02\x13V1\\Services\\Runtime\xe2\x02\x1fV1\\Services\\Runtime\\GPBMetadata\xea\x02\x15V1::Services::Runtimeb\x06proto3')

_globals = globals()
_builder.BuildMessageAndEnumDescriptors(DESCRIPTOR, _globals)
_builder.BuildTopDescriptorsAndMessages(DESCRIPTOR, 'proto.services.runtime.tracker_pb2', _globals)
if _descriptor._USE_C_DESCRIPTORS == False:
  _globals['DESCRIPTOR']._options = None
  _globals['DESCRIPTOR']._serialized_options = b'\n\027com.v1.services.runtimeB\014TrackerProtoP\001ZBgithub.com/codefly-dev/core/generated/v1/go/proto/services/runtime\242\002\003VSR\252\002\023V1.Services.Runtime\312\002\023V1\\Services\\Runtime\342\002\037V1\\Services\\Runtime\\GPBMetadata\352\002\025V1::Services::Runtime'
  _globals['_TRACKERS_TRACKERSENTRY']._options = None
  _globals['_TRACKERS_TRACKERSENTRY']._serialized_options = b'8\001'
  _globals['_PROCESSTRACKER']._serialized_start=61
  _globals['_PROCESSTRACKER']._serialized_end=115
  _globals['_DOCKERTRACKER']._serialized_start=117
  _globals['_DOCKERTRACKER']._serialized_end=167
  _globals['_TRACKER']._serialized_start=170
  _globals['_TRACKER']._serialized_end=367
  _globals['_TRACKERLIST']._serialized_start=369
  _globals['_TRACKERLIST']._serialized_end=440
  _globals['_TRACKERS']._serialized_start=443
  _globals['_TRACKERS']._serialized_end=621
  _globals['_TRACKERS_TRACKERSENTRY']._serialized_start=528
  _globals['_TRACKERS_TRACKERSENTRY']._serialized_end=621
# @@protoc_insertion_point(module_scope)
