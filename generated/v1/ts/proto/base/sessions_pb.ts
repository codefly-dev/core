// @generated by protoc-gen-es v1.4.2 with parameter "target=ts"
// @generated from file proto/base/sessions.proto (package v1.base, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3, Timestamp } from "@bufbuild/protobuf";

/**
 * @generated from message v1.base.ProjectSnapshot
 */
export class ProjectSnapshot extends Message<ProjectSnapshot> {
  /**
   * @generated from field: string uuid = 1;
   */
  uuid = "";

  /**
   * @generated from field: string name = 2;
   */
  name = "";

  constructor(data?: PartialMessage<ProjectSnapshot>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.ProjectSnapshot";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "uuid", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): ProjectSnapshot {
    return new ProjectSnapshot().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): ProjectSnapshot {
    return new ProjectSnapshot().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): ProjectSnapshot {
    return new ProjectSnapshot().fromJsonString(jsonString, options);
  }

  static equals(a: ProjectSnapshot | PlainMessage<ProjectSnapshot> | undefined, b: ProjectSnapshot | PlainMessage<ProjectSnapshot> | undefined): boolean {
    return proto3.util.equals(ProjectSnapshot, a, b);
  }
}

/**
 * @generated from message v1.base.ApplicationSnapshot
 */
export class ApplicationSnapshot extends Message<ApplicationSnapshot> {
  /**
   * @generated from field: string uuid = 1;
   */
  uuid = "";

  /**
   * @generated from field: string name = 2;
   */
  name = "";

  /**
   * @generated from field: v1.base.ProjectSnapshot project = 3;
   */
  project?: ProjectSnapshot;

  constructor(data?: PartialMessage<ApplicationSnapshot>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.ApplicationSnapshot";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "uuid", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "project", kind: "message", T: ProjectSnapshot },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): ApplicationSnapshot {
    return new ApplicationSnapshot().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): ApplicationSnapshot {
    return new ApplicationSnapshot().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): ApplicationSnapshot {
    return new ApplicationSnapshot().fromJsonString(jsonString, options);
  }

  static equals(a: ApplicationSnapshot | PlainMessage<ApplicationSnapshot> | undefined, b: ApplicationSnapshot | PlainMessage<ApplicationSnapshot> | undefined): boolean {
    return proto3.util.equals(ApplicationSnapshot, a, b);
  }
}

/**
 * @generated from message v1.base.PartialSnapshot
 */
export class PartialSnapshot extends Message<PartialSnapshot> {
  /**
   * @generated from field: string uuid = 1;
   */
  uuid = "";

  /**
   * @generated from field: string name = 2;
   */
  name = "";

  /**
   * @generated from field: v1.base.ProjectSnapshot project = 3;
   */
  project?: ProjectSnapshot;

  /**
   * @generated from field: repeated v1.base.ApplicationSnapshot applications = 4;
   */
  applications: ApplicationSnapshot[] = [];

  constructor(data?: PartialMessage<PartialSnapshot>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.PartialSnapshot";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "uuid", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "project", kind: "message", T: ProjectSnapshot },
    { no: 4, name: "applications", kind: "message", T: ApplicationSnapshot, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): PartialSnapshot {
    return new PartialSnapshot().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): PartialSnapshot {
    return new PartialSnapshot().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): PartialSnapshot {
    return new PartialSnapshot().fromJsonString(jsonString, options);
  }

  static equals(a: PartialSnapshot | PlainMessage<PartialSnapshot> | undefined, b: PartialSnapshot | PlainMessage<PartialSnapshot> | undefined): boolean {
    return proto3.util.equals(PartialSnapshot, a, b);
  }
}

/**
 * @generated from message v1.base.Session
 */
export class Session extends Message<Session> {
  /**
   * @generated from field: string uuid = 1;
   */
  uuid = "";

  /**
   * @generated from field: google.protobuf.Timestamp at = 2;
   */
  at?: Timestamp;

  /**
   * @generated from oneof v1.base.Session.session
   */
  session: {
    /**
     * @generated from field: v1.base.PartialSnapshot partial = 3;
     */
    value: PartialSnapshot;
    case: "partial";
  } | {
    /**
     * @generated from field: v1.base.ApplicationSnapshot application = 4;
     */
    value: ApplicationSnapshot;
    case: "application";
  } | { case: undefined; value?: undefined } = { case: undefined };

  constructor(data?: PartialMessage<Session>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.Session";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "uuid", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "at", kind: "message", T: Timestamp },
    { no: 3, name: "partial", kind: "message", T: PartialSnapshot, oneof: "session" },
    { no: 4, name: "application", kind: "message", T: ApplicationSnapshot, oneof: "session" },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Session {
    return new Session().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Session {
    return new Session().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Session {
    return new Session().fromJsonString(jsonString, options);
  }

  static equals(a: Session | PlainMessage<Session> | undefined, b: Session | PlainMessage<Session> | undefined): boolean {
    return proto3.util.equals(Session, a, b);
  }
}

