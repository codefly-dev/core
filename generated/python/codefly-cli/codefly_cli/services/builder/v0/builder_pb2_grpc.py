# Generated by the gRPC Python protocol compiler plugin. DO NOT EDIT!
"""Client and server classes corresponding to protobuf-defined services."""
import grpc

from services.agent.v0 import communicate_pb2 as services_dot_agent_dot_v0_dot_communicate__pb2
from services.builder.v0 import builder_pb2 as services_dot_builder_dot_v0_dot_builder__pb2


class BuilderStub(object):
    """Builder is responsible for:
    - creation
    - Docker build
    - Deployment manifests
    """

    def __init__(self, channel):
        """Constructor.

        Args:
            channel: A grpc.Channel.
        """
        self.Load = channel.unary_unary(
                '/services.builder.v0.Builder/Load',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.LoadRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.LoadResponse.FromString,
                _registered_method=True)
        self.Init = channel.unary_unary(
                '/services.builder.v0.Builder/Init',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.InitRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.InitResponse.FromString,
                _registered_method=True)
        self.Create = channel.unary_unary(
                '/services.builder.v0.Builder/Create',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.CreateRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.CreateResponse.FromString,
                _registered_method=True)
        self.Update = channel.unary_unary(
                '/services.builder.v0.Builder/Update',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.UpdateRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.UpdateResponse.FromString,
                _registered_method=True)
        self.Sync = channel.unary_unary(
                '/services.builder.v0.Builder/Sync',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.SyncRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.SyncResponse.FromString,
                _registered_method=True)
        self.Build = channel.unary_unary(
                '/services.builder.v0.Builder/Build',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.BuildRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.BuildResponse.FromString,
                _registered_method=True)
        self.Deploy = channel.unary_unary(
                '/services.builder.v0.Builder/Deploy',
                request_serializer=services_dot_builder_dot_v0_dot_builder__pb2.DeploymentRequest.SerializeToString,
                response_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.DeploymentResponse.FromString,
                _registered_method=True)
        self.Communicate = channel.unary_unary(
                '/services.builder.v0.Builder/Communicate',
                request_serializer=services_dot_agent_dot_v0_dot_communicate__pb2.Engage.SerializeToString,
                response_deserializer=services_dot_agent_dot_v0_dot_communicate__pb2.InformationRequest.FromString,
                _registered_method=True)


class BuilderServicer(object):
    """Builder is responsible for:
    - creation
    - Docker build
    - Deployment manifests
    """

    def Load(self, request, context):
        """Load the service
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Init(self, request, context):
        """Init
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Create(self, request, context):
        """Affect Code
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Update(self, request, context):
        """Missing associated documentation comment in .proto file."""
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Sync(self, request, context):
        """Affect Data
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Build(self, request, context):
        """Deployment/Build only on init data
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Deploy(self, request, context):
        """Missing associated documentation comment in .proto file."""
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Communicate(self, request, context):
        """Communication helper
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')


def add_BuilderServicer_to_server(servicer, server):
    rpc_method_handlers = {
            'Load': grpc.unary_unary_rpc_method_handler(
                    servicer.Load,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.LoadRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.LoadResponse.SerializeToString,
            ),
            'Init': grpc.unary_unary_rpc_method_handler(
                    servicer.Init,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.InitRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.InitResponse.SerializeToString,
            ),
            'Create': grpc.unary_unary_rpc_method_handler(
                    servicer.Create,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.CreateRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.CreateResponse.SerializeToString,
            ),
            'Update': grpc.unary_unary_rpc_method_handler(
                    servicer.Update,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.UpdateRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.UpdateResponse.SerializeToString,
            ),
            'Sync': grpc.unary_unary_rpc_method_handler(
                    servicer.Sync,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.SyncRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.SyncResponse.SerializeToString,
            ),
            'Build': grpc.unary_unary_rpc_method_handler(
                    servicer.Build,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.BuildRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.BuildResponse.SerializeToString,
            ),
            'Deploy': grpc.unary_unary_rpc_method_handler(
                    servicer.Deploy,
                    request_deserializer=services_dot_builder_dot_v0_dot_builder__pb2.DeploymentRequest.FromString,
                    response_serializer=services_dot_builder_dot_v0_dot_builder__pb2.DeploymentResponse.SerializeToString,
            ),
            'Communicate': grpc.unary_unary_rpc_method_handler(
                    servicer.Communicate,
                    request_deserializer=services_dot_agent_dot_v0_dot_communicate__pb2.Engage.FromString,
                    response_serializer=services_dot_agent_dot_v0_dot_communicate__pb2.InformationRequest.SerializeToString,
            ),
    }
    generic_handler = grpc.method_handlers_generic_handler(
            'services.builder.v0.Builder', rpc_method_handlers)
    server.add_generic_rpc_handlers((generic_handler,))


 # This class is part of an EXPERIMENTAL API.
class Builder(object):
    """Builder is responsible for:
    - creation
    - Docker build
    - Deployment manifests
    """

    @staticmethod
    def Load(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Load',
            services_dot_builder_dot_v0_dot_builder__pb2.LoadRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.LoadResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Init(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Init',
            services_dot_builder_dot_v0_dot_builder__pb2.InitRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.InitResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Create(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Create',
            services_dot_builder_dot_v0_dot_builder__pb2.CreateRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.CreateResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Update(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Update',
            services_dot_builder_dot_v0_dot_builder__pb2.UpdateRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.UpdateResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Sync(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Sync',
            services_dot_builder_dot_v0_dot_builder__pb2.SyncRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.SyncResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Build(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Build',
            services_dot_builder_dot_v0_dot_builder__pb2.BuildRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.BuildResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Deploy(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Deploy',
            services_dot_builder_dot_v0_dot_builder__pb2.DeploymentRequest.SerializeToString,
            services_dot_builder_dot_v0_dot_builder__pb2.DeploymentResponse.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)

    @staticmethod
    def Communicate(request,
            target,
            options=(),
            channel_credentials=None,
            call_credentials=None,
            insecure=False,
            compression=None,
            wait_for_ready=None,
            timeout=None,
            metadata=None):
        return grpc.experimental.unary_unary(
            request,
            target,
            '/services.builder.v0.Builder/Communicate',
            services_dot_agent_dot_v0_dot_communicate__pb2.Engage.SerializeToString,
            services_dot_agent_dot_v0_dot_communicate__pb2.InformationRequest.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)