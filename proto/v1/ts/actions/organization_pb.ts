// @generated by protoc-gen-es v1.4.2 with parameter "target=ts"
// @generated from file actions/organization.proto (package v1.actions, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";

/**
 * @generated from message v1.actions.AddOrganization
 */
export class AddOrganization extends Message<AddOrganization> {
  /**
   * @generated from field: string kind = 1;
   */
  kind = "";

  /**
   * @generated from field: string name = 2;
   */
  name = "";

  /**
   * @generated from field: string domain = 3;
   */
  domain = "";

  constructor(data?: PartialMessage<AddOrganization>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "v1.actions.AddOrganization";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "kind", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "domain", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): AddOrganization {
    return new AddOrganization().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): AddOrganization {
    return new AddOrganization().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): AddOrganization {
    return new AddOrganization().fromJsonString(jsonString, options);
  }

  static equals(a: AddOrganization | PlainMessage<AddOrganization> | undefined, b: AddOrganization | PlainMessage<AddOrganization> | undefined): boolean {
    return proto3.util.equals(AddOrganization, a, b);
  }
}

