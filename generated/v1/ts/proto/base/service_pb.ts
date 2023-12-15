// @generated by protoc-gen-es v1.4.2 with parameter "target=ts"
// @generated from file proto/base/service.proto (package v1.base, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";
import { Endpoint } from "./endpoint_pb.js";

/**
 * @generated from message v1.base.Service
 */
export class Service extends Message<Service> {
  /**
   * @generated from field: string name = 1;
   */
  name = "";

  /**
   * @generated from field: string description = 2;
   */
  description = "";

  /**
   * @generated from field: string application = 3;
   */
  application = "";

  /**
   * @generated from field: repeated v1.base.Endpoint endpoints = 4;
   */
  endpoints: Endpoint[] = [];

  constructor(data?: PartialMessage<Service>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.Service";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "description", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "application", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 4, name: "endpoints", kind: "message", T: Endpoint, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Service {
    return new Service().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Service {
    return new Service().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Service {
    return new Service().fromJsonString(jsonString, options);
  }

  static equals(a: Service | PlainMessage<Service> | undefined, b: Service | PlainMessage<Service> | undefined): boolean {
    return proto3.util.equals(Service, a, b);
  }
}

/**
 * @generated from message v1.base.Version
 */
export class Version extends Message<Version> {
  /**
   * @generated from field: string version = 1;
   */
  version = "";

  constructor(data?: PartialMessage<Version>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.Version";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "version", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Version {
    return new Version().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Version {
    return new Version().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Version {
    return new Version().fromJsonString(jsonString, options);
  }

  static equals(a: Version | PlainMessage<Version> | undefined, b: Version | PlainMessage<Version> | undefined): boolean {
    return proto3.util.equals(Version, a, b);
  }
}

/**
 * @generated from message v1.base.ServiceIdentity
 */
export class ServiceIdentity extends Message<ServiceIdentity> {
  /**
   * The name of the service
   *
   * @generated from field: string name = 1;
   */
  name = "";

  /**
   * The domain of the service
   *
   * @generated from field: string domain = 2;
   */
  domain = "";

  /**
   * The application of the service | logical partitioning
   *
   * @generated from field: string application = 3;
   */
  application = "";

  /**
   * The namespace of the service | resource partitioning
   *
   * @generated from field: string namespace = 4;
   */
  namespace = "";

  /**
   * The location of the service | physical partitioning
   *
   * @generated from field: string location = 5;
   */
  location = "";

  constructor(data?: PartialMessage<ServiceIdentity>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.ServiceIdentity";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "domain", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "application", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 4, name: "namespace", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 5, name: "location", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): ServiceIdentity {
    return new ServiceIdentity().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): ServiceIdentity {
    return new ServiceIdentity().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): ServiceIdentity {
    return new ServiceIdentity().fromJsonString(jsonString, options);
  }

  static equals(a: ServiceIdentity | PlainMessage<ServiceIdentity> | undefined, b: ServiceIdentity | PlainMessage<ServiceIdentity> | undefined): boolean {
    return proto3.util.equals(ServiceIdentity, a, b);
  }
}

