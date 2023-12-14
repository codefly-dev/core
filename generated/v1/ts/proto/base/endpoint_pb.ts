// @generated by protoc-gen-es v1.4.2 with parameter "target=ts"
// @generated from file proto/base/endpoint.proto (package v1.base, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";

/**
 * @generated from enum v1.base.HTTPMethod
 */
export enum HTTPMethod {
  /**
   * @generated from enum value: GET = 0;
   */
  GET = 0,

  /**
   * @generated from enum value: POST = 1;
   */
  POST = 1,

  /**
   * @generated from enum value: PUT = 2;
   */
  PUT = 2,

  /**
   * @generated from enum value: DELETE = 3;
   */
  DELETE = 3,

  /**
   * @generated from enum value: PATCH = 4;
   */
  PATCH = 4,

  /**
   * @generated from enum value: OPTIONS = 5;
   */
  OPTIONS = 5,

  /**
   * @generated from enum value: HEAD = 6;
   */
  HEAD = 6,

  /**
   * @generated from enum value: CONNECT = 7;
   */
  CONNECT = 7,

  /**
   * @generated from enum value: TRACE = 8;
   */
  TRACE = 8,
}
// Retrieve enum metadata with: proto3.getEnumType(HTTPMethod)
proto3.util.setEnumType(HTTPMethod, "v1.base.HTTPMethod", [
  { no: 0, name: "GET" },
  { no: 1, name: "POST" },
  { no: 2, name: "PUT" },
  { no: 3, name: "DELETE" },
  { no: 4, name: "PATCH" },
  { no: 5, name: "OPTIONS" },
  { no: 6, name: "HEAD" },
  { no: 7, name: "CONNECT" },
  { no: 8, name: "TRACE" },
]);

/**
 * @generated from message v1.base.Endpoint
 */
export class Endpoint extends Message<Endpoint> {
  /**
   * @generated from field: string name = 1;
   */
  name = "";

  /**
   * @generated from field: string description = 2;
   */
  description = "";

  /**
   * @generated from field: string visibility = 3;
   */
  visibility = "";

  /**
   * @generated from field: v1.base.API api = 4;
   */
  api?: API;

  /**
   * @generated from field: string application = 5;
   */
  application = "";

  /**
   * @generated from field: string service = 6;
   */
  service = "";

  /**
   * @generated from field: string namespace = 7;
   */
  namespace = "";

  constructor(data?: PartialMessage<Endpoint>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.Endpoint";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "description", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "visibility", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 4, name: "api", kind: "message", T: API },
    { no: 5, name: "application", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 6, name: "service", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 7, name: "namespace", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Endpoint {
    return new Endpoint().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Endpoint {
    return new Endpoint().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Endpoint {
    return new Endpoint().fromJsonString(jsonString, options);
  }

  static equals(a: Endpoint | PlainMessage<Endpoint> | undefined, b: Endpoint | PlainMessage<Endpoint> | undefined): boolean {
    return proto3.util.equals(Endpoint, a, b);
  }
}

/**
 * @generated from message v1.base.ServiceEndpointGroup
 */
export class ServiceEndpointGroup extends Message<ServiceEndpointGroup> {
  /**
   * @generated from field: string name = 1;
   */
  name = "";

  /**
   * @generated from field: bool public = 2;
   */
  public = false;

  /**
   * @generated from field: repeated v1.base.Endpoint endpoints = 3;
   */
  endpoints: Endpoint[] = [];

  constructor(data?: PartialMessage<ServiceEndpointGroup>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.ServiceEndpointGroup";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "public", kind: "scalar", T: 8 /* ScalarType.BOOL */ },
    { no: 3, name: "endpoints", kind: "message", T: Endpoint, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): ServiceEndpointGroup {
    return new ServiceEndpointGroup().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): ServiceEndpointGroup {
    return new ServiceEndpointGroup().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): ServiceEndpointGroup {
    return new ServiceEndpointGroup().fromJsonString(jsonString, options);
  }

  static equals(a: ServiceEndpointGroup | PlainMessage<ServiceEndpointGroup> | undefined, b: ServiceEndpointGroup | PlainMessage<ServiceEndpointGroup> | undefined): boolean {
    return proto3.util.equals(ServiceEndpointGroup, a, b);
  }
}

/**
 * @generated from message v1.base.ApplicationEndpointGroup
 */
export class ApplicationEndpointGroup extends Message<ApplicationEndpointGroup> {
  /**
   * @generated from field: string name = 1;
   */
  name = "";

  /**
   * @generated from field: bool public = 2;
   */
  public = false;

  /**
   * @generated from field: repeated v1.base.ServiceEndpointGroup service_endpoint_groups = 3;
   */
  serviceEndpointGroups: ServiceEndpointGroup[] = [];

  constructor(data?: PartialMessage<ApplicationEndpointGroup>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.ApplicationEndpointGroup";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "public", kind: "scalar", T: 8 /* ScalarType.BOOL */ },
    { no: 3, name: "service_endpoint_groups", kind: "message", T: ServiceEndpointGroup, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): ApplicationEndpointGroup {
    return new ApplicationEndpointGroup().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): ApplicationEndpointGroup {
    return new ApplicationEndpointGroup().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): ApplicationEndpointGroup {
    return new ApplicationEndpointGroup().fromJsonString(jsonString, options);
  }

  static equals(a: ApplicationEndpointGroup | PlainMessage<ApplicationEndpointGroup> | undefined, b: ApplicationEndpointGroup | PlainMessage<ApplicationEndpointGroup> | undefined): boolean {
    return proto3.util.equals(ApplicationEndpointGroup, a, b);
  }
}

/**
 * @generated from message v1.base.EndpointGroup
 */
export class EndpointGroup extends Message<EndpointGroup> {
  /**
   * @generated from field: repeated v1.base.ApplicationEndpointGroup application_endpoint_group = 2;
   */
  applicationEndpointGroup: ApplicationEndpointGroup[] = [];

  constructor(data?: PartialMessage<EndpointGroup>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.EndpointGroup";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 2, name: "application_endpoint_group", kind: "message", T: ApplicationEndpointGroup, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): EndpointGroup {
    return new EndpointGroup().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): EndpointGroup {
    return new EndpointGroup().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): EndpointGroup {
    return new EndpointGroup().fromJsonString(jsonString, options);
  }

  static equals(a: EndpointGroup | PlainMessage<EndpointGroup> | undefined, b: EndpointGroup | PlainMessage<EndpointGroup> | undefined): boolean {
    return proto3.util.equals(EndpointGroup, a, b);
  }
}

/**
 * @generated from message v1.base.API
 */
export class API extends Message<API> {
  /**
   * @generated from oneof v1.base.API.value
   */
  value: {
    /**
     * @generated from field: v1.base.TcpAPI tcp = 1;
     */
    value: TcpAPI;
    case: "tcp";
  } | {
    /**
     * @generated from field: v1.base.RestAPI rest = 2;
     */
    value: RestAPI;
    case: "rest";
  } | {
    /**
     * @generated from field: v1.base.GrpcAPI grpc = 3;
     */
    value: GrpcAPI;
    case: "grpc";
  } | { case: undefined; value?: undefined } = { case: undefined };

  constructor(data?: PartialMessage<API>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.API";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "tcp", kind: "message", T: TcpAPI, oneof: "value" },
    { no: 2, name: "rest", kind: "message", T: RestAPI, oneof: "value" },
    { no: 3, name: "grpc", kind: "message", T: GrpcAPI, oneof: "value" },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): API {
    return new API().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): API {
    return new API().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): API {
    return new API().fromJsonString(jsonString, options);
  }

  static equals(a: API | PlainMessage<API> | undefined, b: API | PlainMessage<API> | undefined): boolean {
    return proto3.util.equals(API, a, b);
  }
}

/**
 * @generated from message v1.base.RestRoute
 */
export class RestRoute extends Message<RestRoute> {
  /**
   * @generated from field: repeated v1.base.HTTPMethod methods = 1;
   */
  methods: HTTPMethod[] = [];

  /**
   * @generated from field: string path = 2;
   */
  path = "";

  constructor(data?: PartialMessage<RestRoute>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.RestRoute";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "methods", kind: "enum", T: proto3.getEnumType(HTTPMethod), repeated: true },
    { no: 2, name: "path", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): RestRoute {
    return new RestRoute().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): RestRoute {
    return new RestRoute().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): RestRoute {
    return new RestRoute().fromJsonString(jsonString, options);
  }

  static equals(a: RestRoute | PlainMessage<RestRoute> | undefined, b: RestRoute | PlainMessage<RestRoute> | undefined): boolean {
    return proto3.util.equals(RestRoute, a, b);
  }
}

/**
 * @generated from message v1.base.RestAPI
 */
export class RestAPI extends Message<RestAPI> {
  /**
   * @generated from field: bytes openapi = 1;
   */
  openapi = new Uint8Array(0);

  /**
   * @generated from field: repeated v1.base.RestRoute routes = 2;
   */
  routes: RestRoute[] = [];

  constructor(data?: PartialMessage<RestAPI>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.RestAPI";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "openapi", kind: "scalar", T: 12 /* ScalarType.BYTES */ },
    { no: 2, name: "routes", kind: "message", T: RestRoute, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): RestAPI {
    return new RestAPI().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): RestAPI {
    return new RestAPI().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): RestAPI {
    return new RestAPI().fromJsonString(jsonString, options);
  }

  static equals(a: RestAPI | PlainMessage<RestAPI> | undefined, b: RestAPI | PlainMessage<RestAPI> | undefined): boolean {
    return proto3.util.equals(RestAPI, a, b);
  }
}

/**
 * @generated from message v1.base.RPC
 */
export class RPC extends Message<RPC> {
  /**
   * @generated from field: string name = 1;
   */
  name = "";

  constructor(data?: PartialMessage<RPC>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.RPC";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): RPC {
    return new RPC().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): RPC {
    return new RPC().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): RPC {
    return new RPC().fromJsonString(jsonString, options);
  }

  static equals(a: RPC | PlainMessage<RPC> | undefined, b: RPC | PlainMessage<RPC> | undefined): boolean {
    return proto3.util.equals(RPC, a, b);
  }
}

/**
 * @generated from message v1.base.GrpcAPI
 */
export class GrpcAPI extends Message<GrpcAPI> {
  /**
   * @generated from field: bytes proto = 1;
   */
  proto = new Uint8Array(0);

  /**
   * @generated from field: repeated v1.base.RPC rpcs = 2;
   */
  rpcs: RPC[] = [];

  constructor(data?: PartialMessage<GrpcAPI>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.GrpcAPI";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "proto", kind: "scalar", T: 12 /* ScalarType.BYTES */ },
    { no: 2, name: "rpcs", kind: "message", T: RPC, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): GrpcAPI {
    return new GrpcAPI().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): GrpcAPI {
    return new GrpcAPI().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): GrpcAPI {
    return new GrpcAPI().fromJsonString(jsonString, options);
  }

  static equals(a: GrpcAPI | PlainMessage<GrpcAPI> | undefined, b: GrpcAPI | PlainMessage<GrpcAPI> | undefined): boolean {
    return proto3.util.equals(GrpcAPI, a, b);
  }
}

/**
 * @generated from message v1.base.HttpAPI
 */
export class HttpAPI extends Message<HttpAPI> {
  constructor(data?: PartialMessage<HttpAPI>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.HttpAPI";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): HttpAPI {
    return new HttpAPI().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): HttpAPI {
    return new HttpAPI().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): HttpAPI {
    return new HttpAPI().fromJsonString(jsonString, options);
  }

  static equals(a: HttpAPI | PlainMessage<HttpAPI> | undefined, b: HttpAPI | PlainMessage<HttpAPI> | undefined): boolean {
    return proto3.util.equals(HttpAPI, a, b);
  }
}

/**
 * @generated from message v1.base.TcpAPI
 */
export class TcpAPI extends Message<TcpAPI> {
  constructor(data?: PartialMessage<TcpAPI>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.base.TcpAPI";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TcpAPI {
    return new TcpAPI().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TcpAPI {
    return new TcpAPI().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TcpAPI {
    return new TcpAPI().fromJsonString(jsonString, options);
  }

  static equals(a: TcpAPI | PlainMessage<TcpAPI> | undefined, b: TcpAPI | PlainMessage<TcpAPI> | undefined): boolean {
    return proto3.util.equals(TcpAPI, a, b);
  }
}
