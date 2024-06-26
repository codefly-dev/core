# Generated by the gRPC Python protocol compiler plugin. DO NOT EDIT!
"""Client and server classes corresponding to protobuf-defined services."""
import grpc

from codefly.services.agent.v0 import communicate_pb2 as codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2
from codefly.services.runtime.v0 import runtime_pb2 as codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2


class RuntimeStub(object):
    """
    Public API



    Runtime service

    """

    def __init__(self, channel):
        """Constructor.

        Args:
            channel: A grpc.Channel.
        """
        self.Load = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Load',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.LoadRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.LoadResponse.FromString,
                _registered_method=True)
        self.Init = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Init',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InitRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InitResponse.FromString,
                _registered_method=True)
        self.Start = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Start',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StartRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StartResponse.FromString,
                _registered_method=True)
        self.Stop = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Stop',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StopRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StopResponse.FromString,
                _registered_method=True)
        self.Destroy = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Destroy',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.DestroyRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.DestroyResponse.FromString,
                _registered_method=True)
        self.Test = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Test',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.TestRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.TestResponse.FromString,
                _registered_method=True)
        self.Information = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Information',
                request_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InformationRequest.SerializeToString,
                response_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InformationResponse.FromString,
                _registered_method=True)
        self.Communicate = channel.unary_unary(
                '/codefly.services.runtime.v0.Runtime/Communicate',
                request_serializer=codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2.Engage.SerializeToString,
                response_deserializer=codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2.InformationRequest.FromString,
                _registered_method=True)


class RuntimeServicer(object):
    """
    Public API



    Runtime service

    """

    def Load(self, request, context):
        """Lifecycle

        Load the Service Agent: this should be a NoOp and never fails
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Init(self, request, context):
        """Init the Service Agent: could include steps like compilation, configuration, etc.
        An important step of Initialization is to get the list of network mappings
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Start(self, request, context):
        """Start the underlying service
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Stop(self, request, context):
        """Stop the underlying service
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Destroy(self, request, context):
        """Destroy the underlying service
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Test(self, request, context):
        """Test the service
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Information(self, request, context):
        """Information about the state of the service
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')

    def Communicate(self, request, context):
        """Communication helper
        """
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details('Method not implemented!')
        raise NotImplementedError('Method not implemented!')


def add_RuntimeServicer_to_server(servicer, server):
    rpc_method_handlers = {
            'Load': grpc.unary_unary_rpc_method_handler(
                    servicer.Load,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.LoadRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.LoadResponse.SerializeToString,
            ),
            'Init': grpc.unary_unary_rpc_method_handler(
                    servicer.Init,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InitRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InitResponse.SerializeToString,
            ),
            'Start': grpc.unary_unary_rpc_method_handler(
                    servicer.Start,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StartRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StartResponse.SerializeToString,
            ),
            'Stop': grpc.unary_unary_rpc_method_handler(
                    servicer.Stop,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StopRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StopResponse.SerializeToString,
            ),
            'Destroy': grpc.unary_unary_rpc_method_handler(
                    servicer.Destroy,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.DestroyRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.DestroyResponse.SerializeToString,
            ),
            'Test': grpc.unary_unary_rpc_method_handler(
                    servicer.Test,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.TestRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.TestResponse.SerializeToString,
            ),
            'Information': grpc.unary_unary_rpc_method_handler(
                    servicer.Information,
                    request_deserializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InformationRequest.FromString,
                    response_serializer=codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InformationResponse.SerializeToString,
            ),
            'Communicate': grpc.unary_unary_rpc_method_handler(
                    servicer.Communicate,
                    request_deserializer=codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2.Engage.FromString,
                    response_serializer=codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2.InformationRequest.SerializeToString,
            ),
    }
    generic_handler = grpc.method_handlers_generic_handler(
            'codefly.services.runtime.v0.Runtime', rpc_method_handlers)
    server.add_generic_rpc_handlers((generic_handler,))
    server.add_registered_method_handlers('codefly.services.runtime.v0.Runtime', rpc_method_handlers)


 # This class is part of an EXPERIMENTAL API.
class Runtime(object):
    """
    Public API



    Runtime service

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
            '/codefly.services.runtime.v0.Runtime/Load',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.LoadRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.LoadResponse.FromString,
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
            '/codefly.services.runtime.v0.Runtime/Init',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InitRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InitResponse.FromString,
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
    def Start(request,
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
            '/codefly.services.runtime.v0.Runtime/Start',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StartRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StartResponse.FromString,
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
    def Stop(request,
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
            '/codefly.services.runtime.v0.Runtime/Stop',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StopRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.StopResponse.FromString,
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
    def Destroy(request,
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
            '/codefly.services.runtime.v0.Runtime/Destroy',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.DestroyRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.DestroyResponse.FromString,
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
    def Test(request,
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
            '/codefly.services.runtime.v0.Runtime/Test',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.TestRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.TestResponse.FromString,
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
    def Information(request,
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
            '/codefly.services.runtime.v0.Runtime/Information',
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InformationRequest.SerializeToString,
            codefly_dot_services_dot_runtime_dot_v0_dot_runtime__pb2.InformationResponse.FromString,
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
            '/codefly.services.runtime.v0.Runtime/Communicate',
            codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2.Engage.SerializeToString,
            codefly_dot_services_dot_agent_dot_v0_dot_communicate__pb2.InformationRequest.FromString,
            options,
            channel_credentials,
            insecure,
            call_credentials,
            compression,
            wait_for_ready,
            timeout,
            metadata,
            _registered_method=True)
