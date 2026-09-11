from codefly.services.builder.v0 import builder_pb2, docker_pb2


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
