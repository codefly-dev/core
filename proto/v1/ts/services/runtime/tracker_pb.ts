// @generated by protoc-gen-es v1.4.2 with parameter "target=ts"
// @generated from file services/runtime/tracker.proto (package v1.services.runtime, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";

/**
 * @generated from message v1.services.runtime.ProcessTracker
 */
export class ProcessTracker extends Message<ProcessTracker> {
  /**
   * @generated from field: int32 PID = 1;
   */
  PID = 0;

  /**
   * @generated from field: string type = 2;
   */
  type = "";

  constructor(data?: PartialMessage<ProcessTracker>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.runtime.ProcessTracker";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "PID", kind: "scalar", T: 5 /* ScalarType.INT32 */ },
    { no: 2, name: "type", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): ProcessTracker {
    return new ProcessTracker().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): ProcessTracker {
    return new ProcessTracker().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): ProcessTracker {
    return new ProcessTracker().fromJsonString(jsonString, options);
  }

  static equals(a: ProcessTracker | PlainMessage<ProcessTracker> | undefined, b: ProcessTracker | PlainMessage<ProcessTracker> | undefined): boolean {
    return proto3.util.equals(ProcessTracker, a, b);
  }
}

/**
 * @generated from message v1.services.runtime.DockerTracker
 */
export class DockerTracker extends Message<DockerTracker> {
  /**
   * @generated from field: string container_id = 1;
   */
  containerId = "";

  constructor(data?: PartialMessage<DockerTracker>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.runtime.DockerTracker";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "container_id", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): DockerTracker {
    return new DockerTracker().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): DockerTracker {
    return new DockerTracker().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): DockerTracker {
    return new DockerTracker().fromJsonString(jsonString, options);
  }

  static equals(a: DockerTracker | PlainMessage<DockerTracker> | undefined, b: DockerTracker | PlainMessage<DockerTracker> | undefined): boolean {
    return proto3.util.equals(DockerTracker, a, b);
  }
}

/**
 * @generated from message v1.services.runtime.Tracker
 */
export class Tracker extends Message<Tracker> {
  /**
   * @generated from field: string name = 1;
   */
  name = "";

  /**
   * @generated from oneof v1.services.runtime.Tracker.tracker
   */
  tracker: {
    /**
     * @generated from field: v1.services.runtime.ProcessTracker process_tracker = 4;
     */
    value: ProcessTracker;
    case: "processTracker";
  } | {
    /**
     * @generated from field: v1.services.runtime.DockerTracker docker_tracker = 5;
     */
    value: DockerTracker;
    case: "dockerTracker";
  } | { case: undefined; value?: undefined } = { case: undefined };

  constructor(data?: PartialMessage<Tracker>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.runtime.Tracker";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 4, name: "process_tracker", kind: "message", T: ProcessTracker, oneof: "tracker" },
    { no: 5, name: "docker_tracker", kind: "message", T: DockerTracker, oneof: "tracker" },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Tracker {
    return new Tracker().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Tracker {
    return new Tracker().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Tracker {
    return new Tracker().fromJsonString(jsonString, options);
  }

  static equals(a: Tracker | PlainMessage<Tracker> | undefined, b: Tracker | PlainMessage<Tracker> | undefined): boolean {
    return proto3.util.equals(Tracker, a, b);
  }
}

/**
 * @generated from message v1.services.runtime.TrackerList
 */
export class TrackerList extends Message<TrackerList> {
  /**
   * @generated from field: repeated v1.services.runtime.Tracker trackers = 1;
   */
  trackers: Tracker[] = [];

  constructor(data?: PartialMessage<TrackerList>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.runtime.TrackerList";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "trackers", kind: "message", T: Tracker, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TrackerList {
    return new TrackerList().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TrackerList {
    return new TrackerList().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TrackerList {
    return new TrackerList().fromJsonString(jsonString, options);
  }

  static equals(a: TrackerList | PlainMessage<TrackerList> | undefined, b: TrackerList | PlainMessage<TrackerList> | undefined): boolean {
    return proto3.util.equals(TrackerList, a, b);
  }
}

/**
 * @generated from message v1.services.runtime.Trackers
 */
export class Trackers extends Message<Trackers> {
  /**
   * @generated from field: map<string, v1.services.runtime.TrackerList> trackers = 1;
   */
  trackers: { [key: string]: TrackerList } = {};

  constructor(data?: PartialMessage<Trackers>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.runtime.Trackers";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "trackers", kind: "map", K: 9 /* ScalarType.STRING */, V: {kind: "message", T: TrackerList} },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Trackers {
    return new Trackers().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Trackers {
    return new Trackers().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Trackers {
    return new Trackers().fromJsonString(jsonString, options);
  }

  static equals(a: Trackers | PlainMessage<Trackers> | undefined, b: Trackers | PlainMessage<Trackers> | undefined): boolean {
    return proto3.util.equals(Trackers, a, b);
  }
}

