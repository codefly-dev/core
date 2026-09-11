from concurrent.futures import ThreadPoolExecutor

import grpc

from codefly.services.builder.v0 import builder_pb2, builder_pb2_grpc, docker_pb2


def test_cache_policy_round_trip_remains_caller_owned():
    cache = docker_pb2.BuildCacheOptions(
        backend="registry", scope="service", exports=["ghcr.io/org/cache"]
    )
    request = builder_pb2.BuildRequest(
        build_context=builder_pb2.BuildContext(
            docker_build_context=docker_pb2.DockerBuildContext(cache=cache)
        )
    )
    restored = builder_pb2.BuildRequest.FromString(request.SerializeToString())
    assert restored.build_context.docker_build_context.cache == cache
    assert "cache" not in docker_pb2.DockerBuildRecipe.DESCRIPTOR.fields_by_name
    response = builder_pb2.BuildResponse(cache_contract_version="registry-v1")
    assert response.cache_contract_version == "registry-v1"


def test_buildx_selection_round_trip():
    request = builder_pb2.BuildRequest(
        build_context=builder_pb2.BuildContext(
            docker_build_context=docker_pb2.DockerBuildContext(buildx_builder="selected")
        )
    )
    restored = builder_pb2.BuildRequest.FromString(request.SerializeToString())
    assert restored.build_context.docker_build_context.buildx_builder == "selected"
    response = builder_pb2.BuildResponse(buildx_builder="selected")
    assert builder_pb2.BuildResponse.FromString(response.SerializeToString()).buildx_builder == "selected"


def test_buildx_capability_defaults_to_unsupported():
    assert not builder_pb2.BuildCapabilitiesResponse().buildx_selection
    response = builder_pb2.BuildCapabilitiesResponse(buildx_selection=True)
    assert builder_pb2.BuildCapabilitiesResponse.FromString(
        response.SerializeToString()
    ).buildx_selection
    method = builder_pb2.DESCRIPTOR.services_by_name["Builder"].methods_by_name[
        "BuildCapabilities"
    ]
    assert method.input_type == builder_pb2.BuildCapabilitiesRequest.DESCRIPTOR
    assert method.output_type == builder_pb2.BuildCapabilitiesResponse.DESCRIPTOR


def test_buildx_capability_rpc_round_trip():
    class SupportingBuilder(builder_pb2_grpc.BuilderServicer):
        def BuildCapabilities(self, request, context):
            return builder_pb2.BuildCapabilitiesResponse(buildx_selection=True)

    with ThreadPoolExecutor(max_workers=1) as executor:
        server = grpc.server(executor)
        builder_pb2_grpc.add_BuilderServicer_to_server(SupportingBuilder(), server)
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        try:
            with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                response = builder_pb2_grpc.BuilderStub(channel).BuildCapabilities(
                    builder_pb2.BuildCapabilitiesRequest(), timeout=5
                )
                assert response.buildx_selection
        finally:
            server.stop(0).wait()
