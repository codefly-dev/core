// @generated by protoc-gen-es v1.4.2 with parameter "target=ts"
// @generated from file services/factory/factory.proto (package v1.services.factory, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";
import { InitStatus, Version } from "../init_pb.js";
import { Endpoint, EndpointGroup } from "../../base/api_pb.js";
import { Channel } from "../../agents/communicate_pb.js";
import { Environment } from "../../base/environment_pb.js";

/**
 * @generated from message v1.services.factory.InitResponse
 */
export class InitResponse extends Message<InitResponse> {
  /**
   * @generated from field: v1.services.Version version = 1;
   */
  version?: Version;

  /**
   * @generated from field: repeated v1.base.Endpoint endpoints = 2;
   */
  endpoints: Endpoint[] = [];

  /**
   * The communication channels of the service
   *
   * @generated from field: repeated v1.agents.communicate.Channel channels = 3;
   */
  channels: Channel[] = [];

  /**
   * @generated from field: string read_me = 4;
   */
  readMe = "";

  /**
   * @generated from field: v1.services.InitStatus status = 5;
   */
  status?: InitStatus;

  constructor(data?: PartialMessage<InitResponse>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.InitResponse";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "version", kind: "message", T: Version },
    { no: 2, name: "endpoints", kind: "message", T: Endpoint, repeated: true },
    { no: 3, name: "channels", kind: "message", T: Channel, repeated: true },
    { no: 4, name: "read_me", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 5, name: "status", kind: "message", T: InitStatus },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): InitResponse {
    return new InitResponse().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): InitResponse {
    return new InitResponse().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): InitResponse {
    return new InitResponse().fromJsonString(jsonString, options);
  }

  static equals(a: InitResponse | PlainMessage<InitResponse> | undefined, b: InitResponse | PlainMessage<InitResponse> | undefined): boolean {
    return proto3.util.equals(InitResponse, a, b);
  }
}

/**
 * @generated from message v1.services.factory.CreateRequest
 */
export class CreateRequest extends Message<CreateRequest> {
  constructor(data?: PartialMessage<CreateRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.CreateRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): CreateRequest {
    return new CreateRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): CreateRequest {
    return new CreateRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): CreateRequest {
    return new CreateRequest().fromJsonString(jsonString, options);
  }

  static equals(a: CreateRequest | PlainMessage<CreateRequest> | undefined, b: CreateRequest | PlainMessage<CreateRequest> | undefined): boolean {
    return proto3.util.equals(CreateRequest, a, b);
  }
}

/**
 * @generated from message v1.services.factory.CreateResponse
 */
export class CreateResponse extends Message<CreateResponse> {
  /**
   * @generated from field: bool need_communication = 1;
   */
  needCommunication = false;

  /**
   * The endpoints of the created service
   *
   * @generated from field: repeated v1.base.Endpoint endpoints = 2;
   */
  endpoints: Endpoint[] = [];

  constructor(data?: PartialMessage<CreateResponse>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.CreateResponse";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "need_communication", kind: "scalar", T: 8 /* ScalarType.BOOL */ },
    { no: 2, name: "endpoints", kind: "message", T: Endpoint, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): CreateResponse {
    return new CreateResponse().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): CreateResponse {
    return new CreateResponse().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): CreateResponse {
    return new CreateResponse().fromJsonString(jsonString, options);
  }

  static equals(a: CreateResponse | PlainMessage<CreateResponse> | undefined, b: CreateResponse | PlainMessage<CreateResponse> | undefined): boolean {
    return proto3.util.equals(CreateResponse, a, b);
  }
}

/**
 * @generated from message v1.services.factory.UpdateRequest
 */
export class UpdateRequest extends Message<UpdateRequest> {
  constructor(data?: PartialMessage<UpdateRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.UpdateRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): UpdateRequest {
    return new UpdateRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): UpdateRequest {
    return new UpdateRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): UpdateRequest {
    return new UpdateRequest().fromJsonString(jsonString, options);
  }

  static equals(a: UpdateRequest | PlainMessage<UpdateRequest> | undefined, b: UpdateRequest | PlainMessage<UpdateRequest> | undefined): boolean {
    return proto3.util.equals(UpdateRequest, a, b);
  }
}

/**
 * @generated from message v1.services.factory.UpdateResponse
 */
export class UpdateResponse extends Message<UpdateResponse> {
  constructor(data?: PartialMessage<UpdateResponse>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.UpdateResponse";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): UpdateResponse {
    return new UpdateResponse().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): UpdateResponse {
    return new UpdateResponse().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): UpdateResponse {
    return new UpdateResponse().fromJsonString(jsonString, options);
  }

  static equals(a: UpdateResponse | PlainMessage<UpdateResponse> | undefined, b: UpdateResponse | PlainMessage<UpdateResponse> | undefined): boolean {
    return proto3.util.equals(UpdateResponse, a, b);
  }
}

/**
 * @generated from message v1.services.factory.SyncRequest
 */
export class SyncRequest extends Message<SyncRequest> {
  /**
   * @generated from field: v1.base.EndpointGroup dependency_endpoint_group = 1;
   */
  dependencyEndpointGroup?: EndpointGroup;

  constructor(data?: PartialMessage<SyncRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.SyncRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "dependency_endpoint_group", kind: "message", T: EndpointGroup },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): SyncRequest {
    return new SyncRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): SyncRequest {
    return new SyncRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): SyncRequest {
    return new SyncRequest().fromJsonString(jsonString, options);
  }

  static equals(a: SyncRequest | PlainMessage<SyncRequest> | undefined, b: SyncRequest | PlainMessage<SyncRequest> | undefined): boolean {
    return proto3.util.equals(SyncRequest, a, b);
  }
}

/**
 * @generated from message v1.services.factory.SyncResponse
 */
export class SyncResponse extends Message<SyncResponse> {
  /**
   * @generated from field: bool need_communication = 1;
   */
  needCommunication = false;

  constructor(data?: PartialMessage<SyncResponse>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.SyncResponse";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "need_communication", kind: "scalar", T: 8 /* ScalarType.BOOL */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): SyncResponse {
    return new SyncResponse().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): SyncResponse {
    return new SyncResponse().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): SyncResponse {
    return new SyncResponse().fromJsonString(jsonString, options);
  }

  static equals(a: SyncResponse | PlainMessage<SyncResponse> | undefined, b: SyncResponse | PlainMessage<SyncResponse> | undefined): boolean {
    return proto3.util.equals(SyncResponse, a, b);
  }
}

/**
 * @generated from message v1.services.factory.BuildRequest
 */
export class BuildRequest extends Message<BuildRequest> {
  /**
   * @generated from field: v1.base.EndpointGroup dependency_endpoint_group = 1;
   */
  dependencyEndpointGroup?: EndpointGroup;

  constructor(data?: PartialMessage<BuildRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.BuildRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "dependency_endpoint_group", kind: "message", T: EndpointGroup },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): BuildRequest {
    return new BuildRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): BuildRequest {
    return new BuildRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): BuildRequest {
    return new BuildRequest().fromJsonString(jsonString, options);
  }

  static equals(a: BuildRequest | PlainMessage<BuildRequest> | undefined, b: BuildRequest | PlainMessage<BuildRequest> | undefined): boolean {
    return proto3.util.equals(BuildRequest, a, b);
  }
}

/**
 * @generated from message v1.services.factory.BuildResponse
 */
export class BuildResponse extends Message<BuildResponse> {
  constructor(data?: PartialMessage<BuildResponse>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.BuildResponse";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): BuildResponse {
    return new BuildResponse().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): BuildResponse {
    return new BuildResponse().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): BuildResponse {
    return new BuildResponse().fromJsonString(jsonString, options);
  }

  static equals(a: BuildResponse | PlainMessage<BuildResponse> | undefined, b: BuildResponse | PlainMessage<BuildResponse> | undefined): boolean {
    return proto3.util.equals(BuildResponse, a, b);
  }
}

/**
 * @generated from message v1.services.factory.DeploymentRequest
 */
export class DeploymentRequest extends Message<DeploymentRequest> {
  /**
   * @generated from field: v1.base.Environment environment = 1;
   */
  environment?: Environment;

  /**
   * @generated from field: v1.base.EndpointGroup dependency_endpoint_group = 2;
   */
  dependencyEndpointGroup?: EndpointGroup;

  constructor(data?: PartialMessage<DeploymentRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.DeploymentRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "environment", kind: "message", T: Environment },
    { no: 2, name: "dependency_endpoint_group", kind: "message", T: EndpointGroup },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): DeploymentRequest {
    return new DeploymentRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): DeploymentRequest {
    return new DeploymentRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): DeploymentRequest {
    return new DeploymentRequest().fromJsonString(jsonString, options);
  }

  static equals(a: DeploymentRequest | PlainMessage<DeploymentRequest> | undefined, b: DeploymentRequest | PlainMessage<DeploymentRequest> | undefined): boolean {
    return proto3.util.equals(DeploymentRequest, a, b);
  }
}

/**
 * @generated from message v1.services.factory.DeploymentResponse
 */
export class DeploymentResponse extends Message<DeploymentResponse> {
  constructor(data?: PartialMessage<DeploymentResponse>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.services.factory.DeploymentResponse";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): DeploymentResponse {
    return new DeploymentResponse().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): DeploymentResponse {
    return new DeploymentResponse().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): DeploymentResponse {
    return new DeploymentResponse().fromJsonString(jsonString, options);
  }

  static equals(a: DeploymentResponse | PlainMessage<DeploymentResponse> | undefined, b: DeploymentResponse | PlainMessage<DeploymentResponse> | undefined): boolean {
    return proto3.util.equals(DeploymentResponse, a, b);
  }
}

