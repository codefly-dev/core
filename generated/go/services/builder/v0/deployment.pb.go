// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.0
// 	protoc        (unknown)
// source: services/builder/v0/deployment.proto

package v0

import (
	reflect "reflect"
	sync "sync"

	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type DeploymentKind int32

const (
	DeploymentKind_KUSTOMIZE DeploymentKind = 0
)

// Enum value maps for DeploymentKind.
var (
	DeploymentKind_name = map[int32]string{
		0: "KUSTOMIZE",
	}
	DeploymentKind_value = map[string]int32{
		"KUSTOMIZE": 0,
	}
)

func (x DeploymentKind) Enum() *DeploymentKind {
	p := new(DeploymentKind)
	*p = x
	return p
}

func (x DeploymentKind) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (DeploymentKind) Descriptor() protoreflect.EnumDescriptor {
	return file_services_builder_v0_deployment_proto_enumTypes[0].Descriptor()
}

func (DeploymentKind) Type() protoreflect.EnumType {
	return &file_services_builder_v0_deployment_proto_enumTypes[0]
}

func (x DeploymentKind) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use DeploymentKind.Descriptor instead.
func (DeploymentKind) EnumDescriptor() ([]byte, []int) {
	return file_services_builder_v0_deployment_proto_rawDescGZIP(), []int{0}
}

type KubernetesDeploymentOutput_Kind int32

const (
	KubernetesDeploymentOutput_Kustomize KubernetesDeploymentOutput_Kind = 0
)

// Enum value maps for KubernetesDeploymentOutput_Kind.
var (
	KubernetesDeploymentOutput_Kind_name = map[int32]string{
		0: "Kustomize",
	}
	KubernetesDeploymentOutput_Kind_value = map[string]int32{
		"Kustomize": 0,
	}
)

func (x KubernetesDeploymentOutput_Kind) Enum() *KubernetesDeploymentOutput_Kind {
	p := new(KubernetesDeploymentOutput_Kind)
	*p = x
	return p
}

func (x KubernetesDeploymentOutput_Kind) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (KubernetesDeploymentOutput_Kind) Descriptor() protoreflect.EnumDescriptor {
	return file_services_builder_v0_deployment_proto_enumTypes[1].Descriptor()
}

func (KubernetesDeploymentOutput_Kind) Type() protoreflect.EnumType {
	return &file_services_builder_v0_deployment_proto_enumTypes[1]
}

func (x KubernetesDeploymentOutput_Kind) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use KubernetesDeploymentOutput_Kind.Descriptor instead.
func (KubernetesDeploymentOutput_Kind) EnumDescriptor() ([]byte, []int) {
	return file_services_builder_v0_deployment_proto_rawDescGZIP(), []int{3, 0}
}

type Deployment struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to Kind:
	//
	//	*Deployment_Kubernetes
	Kind         isDeployment_Kind `protobuf_oneof:"kind"`
	LoadBalancer bool              `protobuf:"varint,3,opt,name=load_balancer,json=loadBalancer,proto3" json:"load_balancer,omitempty"`
}

func (x *Deployment) Reset() {
	*x = Deployment{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_builder_v0_deployment_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Deployment) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Deployment) ProtoMessage() {}

func (x *Deployment) ProtoReflect() protoreflect.Message {
	mi := &file_services_builder_v0_deployment_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Deployment.ProtoReflect.Descriptor instead.
func (*Deployment) Descriptor() ([]byte, []int) {
	return file_services_builder_v0_deployment_proto_rawDescGZIP(), []int{0}
}

func (m *Deployment) GetKind() isDeployment_Kind {
	if m != nil {
		return m.Kind
	}
	return nil
}

func (x *Deployment) GetKubernetes() *KubernetesDeployment {
	if x, ok := x.GetKind().(*Deployment_Kubernetes); ok {
		return x.Kubernetes
	}
	return nil
}

func (x *Deployment) GetLoadBalancer() bool {
	if x != nil {
		return x.LoadBalancer
	}
	return false
}

type isDeployment_Kind interface {
	isDeployment_Kind()
}

type Deployment_Kubernetes struct {
	Kubernetes *KubernetesDeployment `protobuf:"bytes,2,opt,name=kubernetes,proto3,oneof"`
}

func (*Deployment_Kubernetes) isDeployment_Kind() {}

type KubernetesDeployment struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Namespace    string              `protobuf:"bytes,1,opt,name=namespace,proto3" json:"namespace,omitempty"`
	Destination  string              `protobuf:"bytes,2,opt,name=destination,proto3" json:"destination,omitempty"`
	BuildContext *DockerBuildContext `protobuf:"bytes,3,opt,name=build_context,json=buildContext,proto3" json:"build_context,omitempty"`
}

func (x *KubernetesDeployment) Reset() {
	*x = KubernetesDeployment{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_builder_v0_deployment_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *KubernetesDeployment) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*KubernetesDeployment) ProtoMessage() {}

func (x *KubernetesDeployment) ProtoReflect() protoreflect.Message {
	mi := &file_services_builder_v0_deployment_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use KubernetesDeployment.ProtoReflect.Descriptor instead.
func (*KubernetesDeployment) Descriptor() ([]byte, []int) {
	return file_services_builder_v0_deployment_proto_rawDescGZIP(), []int{1}
}

func (x *KubernetesDeployment) GetNamespace() string {
	if x != nil {
		return x.Namespace
	}
	return ""
}

func (x *KubernetesDeployment) GetDestination() string {
	if x != nil {
		return x.Destination
	}
	return ""
}

func (x *KubernetesDeployment) GetBuildContext() *DockerBuildContext {
	if x != nil {
		return x.BuildContext
	}
	return nil
}

type DeploymentOutput struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to Kind:
	//
	//	*DeploymentOutput_Kubernetes
	Kind isDeploymentOutput_Kind `protobuf_oneof:"kind"`
}

func (x *DeploymentOutput) Reset() {
	*x = DeploymentOutput{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_builder_v0_deployment_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DeploymentOutput) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeploymentOutput) ProtoMessage() {}

func (x *DeploymentOutput) ProtoReflect() protoreflect.Message {
	mi := &file_services_builder_v0_deployment_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeploymentOutput.ProtoReflect.Descriptor instead.
func (*DeploymentOutput) Descriptor() ([]byte, []int) {
	return file_services_builder_v0_deployment_proto_rawDescGZIP(), []int{2}
}

func (m *DeploymentOutput) GetKind() isDeploymentOutput_Kind {
	if m != nil {
		return m.Kind
	}
	return nil
}

func (x *DeploymentOutput) GetKubernetes() *KubernetesDeploymentOutput {
	if x, ok := x.GetKind().(*DeploymentOutput_Kubernetes); ok {
		return x.Kubernetes
	}
	return nil
}

type isDeploymentOutput_Kind interface {
	isDeploymentOutput_Kind()
}

type DeploymentOutput_Kubernetes struct {
	Kubernetes *KubernetesDeploymentOutput `protobuf:"bytes,2,opt,name=kubernetes,proto3,oneof"`
}

func (*DeploymentOutput_Kubernetes) isDeploymentOutput_Kind() {}

type KubernetesDeploymentOutput struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Kind KubernetesDeploymentOutput_Kind `protobuf:"varint,1,opt,name=kind,proto3,enum=services.builder.v0.KubernetesDeploymentOutput_Kind" json:"kind,omitempty"`
}

func (x *KubernetesDeploymentOutput) Reset() {
	*x = KubernetesDeploymentOutput{}
	if protoimpl.UnsafeEnabled {
		mi := &file_services_builder_v0_deployment_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *KubernetesDeploymentOutput) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*KubernetesDeploymentOutput) ProtoMessage() {}

func (x *KubernetesDeploymentOutput) ProtoReflect() protoreflect.Message {
	mi := &file_services_builder_v0_deployment_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use KubernetesDeploymentOutput.ProtoReflect.Descriptor instead.
func (*KubernetesDeploymentOutput) Descriptor() ([]byte, []int) {
	return file_services_builder_v0_deployment_proto_rawDescGZIP(), []int{3}
}

func (x *KubernetesDeploymentOutput) GetKind() KubernetesDeploymentOutput_Kind {
	if x != nil {
		return x.Kind
	}
	return KubernetesDeploymentOutput_Kustomize
}

var File_services_builder_v0_deployment_proto protoreflect.FileDescriptor

var file_services_builder_v0_deployment_proto_rawDesc = []byte{
	0x0a, 0x24, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x62, 0x75, 0x69, 0x6c, 0x64,
	0x65, 0x72, 0x2f, 0x76, 0x30, 0x2f, 0x64, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x13, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x1a, 0x20, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x30,
	0x2f, 0x64, 0x6f, 0x63, 0x6b, 0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x86, 0x01,
	0x0a, 0x0a, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74, 0x12, 0x4b, 0x0a, 0x0a,
	0x6b, 0x75, 0x62, 0x65, 0x72, 0x6e, 0x65, 0x74, 0x65, 0x73, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x29, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x62, 0x75, 0x69, 0x6c,
	0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x2e, 0x4b, 0x75, 0x62, 0x65, 0x72, 0x6e, 0x65, 0x74, 0x65,
	0x73, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74, 0x48, 0x00, 0x52, 0x0a, 0x6b,
	0x75, 0x62, 0x65, 0x72, 0x6e, 0x65, 0x74, 0x65, 0x73, 0x12, 0x23, 0x0a, 0x0d, 0x6c, 0x6f, 0x61,
	0x64, 0x5f, 0x62, 0x61, 0x6c, 0x61, 0x6e, 0x63, 0x65, 0x72, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08,
	0x52, 0x0c, 0x6c, 0x6f, 0x61, 0x64, 0x42, 0x61, 0x6c, 0x61, 0x6e, 0x63, 0x65, 0x72, 0x42, 0x06,
	0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x22, 0xa4, 0x01, 0x0a, 0x14, 0x4b, 0x75, 0x62, 0x65, 0x72,
	0x6e, 0x65, 0x74, 0x65, 0x73, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74, 0x12,
	0x1c, 0x0a, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x70, 0x61, 0x63, 0x65, 0x12, 0x20, 0x0a,
	0x0b, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0b, 0x64, 0x65, 0x73, 0x74, 0x69, 0x6e, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12,
	0x4c, 0x0a, 0x0d, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x5f, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x27, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65,
	0x73, 0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x2e, 0x44, 0x6f, 0x63,
	0x6b, 0x65, 0x72, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74, 0x52,
	0x0c, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x78, 0x74, 0x22, 0x6d, 0x0a,
	0x10, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74, 0x4f, 0x75, 0x74, 0x70, 0x75,
	0x74, 0x12, 0x51, 0x0a, 0x0a, 0x6b, 0x75, 0x62, 0x65, 0x72, 0x6e, 0x65, 0x74, 0x65, 0x73, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x2f, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x2e, 0x4b, 0x75, 0x62, 0x65,
	0x72, 0x6e, 0x65, 0x74, 0x65, 0x73, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74,
	0x4f, 0x75, 0x74, 0x70, 0x75, 0x74, 0x48, 0x00, 0x52, 0x0a, 0x6b, 0x75, 0x62, 0x65, 0x72, 0x6e,
	0x65, 0x74, 0x65, 0x73, 0x42, 0x06, 0x0a, 0x04, 0x6b, 0x69, 0x6e, 0x64, 0x22, 0x7d, 0x0a, 0x1a,
	0x4b, 0x75, 0x62, 0x65, 0x72, 0x6e, 0x65, 0x74, 0x65, 0x73, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79,
	0x6d, 0x65, 0x6e, 0x74, 0x4f, 0x75, 0x74, 0x70, 0x75, 0x74, 0x12, 0x48, 0x0a, 0x04, 0x6b, 0x69,
	0x6e, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x34, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x73, 0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x2e, 0x4b,
	0x75, 0x62, 0x65, 0x72, 0x6e, 0x65, 0x74, 0x65, 0x73, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d,
	0x65, 0x6e, 0x74, 0x4f, 0x75, 0x74, 0x70, 0x75, 0x74, 0x2e, 0x4b, 0x69, 0x6e, 0x64, 0x52, 0x04,
	0x6b, 0x69, 0x6e, 0x64, 0x22, 0x15, 0x0a, 0x04, 0x4b, 0x69, 0x6e, 0x64, 0x12, 0x0d, 0x0a, 0x09,
	0x4b, 0x75, 0x73, 0x74, 0x6f, 0x6d, 0x69, 0x7a, 0x65, 0x10, 0x00, 0x2a, 0x1f, 0x0a, 0x0e, 0x44,
	0x65, 0x70, 0x6c, 0x6f, 0x79, 0x6d, 0x65, 0x6e, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x12, 0x0d, 0x0a,
	0x09, 0x4b, 0x55, 0x53, 0x54, 0x4f, 0x4d, 0x49, 0x5a, 0x45, 0x10, 0x00, 0x42, 0xd6, 0x01, 0x0a,
	0x17, 0x63, 0x6f, 0x6d, 0x2e, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x62, 0x75,
	0x69, 0x6c, 0x64, 0x65, 0x72, 0x2e, 0x76, 0x30, 0x42, 0x0f, 0x44, 0x65, 0x70, 0x6c, 0x6f, 0x79,
	0x6d, 0x65, 0x6e, 0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x3c, 0x67, 0x69, 0x74,
	0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x64, 0x65, 0x66, 0x6c, 0x79, 0x2d,
	0x64, 0x65, 0x76, 0x2f, 0x63, 0x6f, 0x72, 0x65, 0x2f, 0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74,
	0x65, 0x64, 0x2f, 0x67, 0x6f, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2f, 0x62,
	0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x2f, 0x76, 0x30, 0xa2, 0x02, 0x03, 0x53, 0x42, 0x56, 0xaa,
	0x02, 0x13, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x2e, 0x42, 0x75, 0x69, 0x6c, 0x64,
	0x65, 0x72, 0x2e, 0x56, 0x30, 0xca, 0x02, 0x13, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73,
	0x5c, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x5c, 0x56, 0x30, 0xe2, 0x02, 0x1f, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x5c, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x65, 0x72, 0x5c, 0x56,
	0x30, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x15,
	0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x3a, 0x3a, 0x42, 0x75, 0x69, 0x6c, 0x64, 0x65,
	0x72, 0x3a, 0x3a, 0x56, 0x30, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_services_builder_v0_deployment_proto_rawDescOnce sync.Once
	file_services_builder_v0_deployment_proto_rawDescData = file_services_builder_v0_deployment_proto_rawDesc
)

func file_services_builder_v0_deployment_proto_rawDescGZIP() []byte {
	file_services_builder_v0_deployment_proto_rawDescOnce.Do(func() {
		file_services_builder_v0_deployment_proto_rawDescData = protoimpl.X.CompressGZIP(file_services_builder_v0_deployment_proto_rawDescData)
	})
	return file_services_builder_v0_deployment_proto_rawDescData
}

var file_services_builder_v0_deployment_proto_enumTypes = make([]protoimpl.EnumInfo, 2)
var file_services_builder_v0_deployment_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_services_builder_v0_deployment_proto_goTypes = []interface{}{
	(DeploymentKind)(0),                  // 0: services.builder.v0.DeploymentKind
	(KubernetesDeploymentOutput_Kind)(0), // 1: services.builder.v0.KubernetesDeploymentOutput.Kind
	(*Deployment)(nil),                   // 2: services.builder.v0.Deployment
	(*KubernetesDeployment)(nil),         // 3: services.builder.v0.KubernetesDeployment
	(*DeploymentOutput)(nil),             // 4: services.builder.v0.DeploymentOutput
	(*KubernetesDeploymentOutput)(nil),   // 5: services.builder.v0.KubernetesDeploymentOutput
	(*DockerBuildContext)(nil),           // 6: services.builder.v0.DockerBuildContext
}
var file_services_builder_v0_deployment_proto_depIdxs = []int32{
	3, // 0: services.builder.v0.Deployment.kubernetes:type_name -> services.builder.v0.KubernetesDeployment
	6, // 1: services.builder.v0.KubernetesDeployment.build_context:type_name -> services.builder.v0.DockerBuildContext
	5, // 2: services.builder.v0.DeploymentOutput.kubernetes:type_name -> services.builder.v0.KubernetesDeploymentOutput
	1, // 3: services.builder.v0.KubernetesDeploymentOutput.kind:type_name -> services.builder.v0.KubernetesDeploymentOutput.Kind
	4, // [4:4] is the sub-list for method output_type
	4, // [4:4] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_services_builder_v0_deployment_proto_init() }
func file_services_builder_v0_deployment_proto_init() {
	if File_services_builder_v0_deployment_proto != nil {
		return
	}
	file_services_builder_v0_docker_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_services_builder_v0_deployment_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Deployment); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_services_builder_v0_deployment_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*KubernetesDeployment); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_services_builder_v0_deployment_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DeploymentOutput); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_services_builder_v0_deployment_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*KubernetesDeploymentOutput); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	file_services_builder_v0_deployment_proto_msgTypes[0].OneofWrappers = []interface{}{
		(*Deployment_Kubernetes)(nil),
	}
	file_services_builder_v0_deployment_proto_msgTypes[2].OneofWrappers = []interface{}{
		(*DeploymentOutput_Kubernetes)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_services_builder_v0_deployment_proto_rawDesc,
			NumEnums:      2,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_services_builder_v0_deployment_proto_goTypes,
		DependencyIndexes: file_services_builder_v0_deployment_proto_depIdxs,
		EnumInfos:         file_services_builder_v0_deployment_proto_enumTypes,
		MessageInfos:      file_services_builder_v0_deployment_proto_msgTypes,
	}.Build()
	File_services_builder_v0_deployment_proto = out.File
	file_services_builder_v0_deployment_proto_rawDesc = nil
	file_services_builder_v0_deployment_proto_goTypes = nil
	file_services_builder_v0_deployment_proto_depIdxs = nil
}
