import os
import sys

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from codefly.services.builder.v0 import deployment_pb2
from codefly.services.builder.v0 import docker_pb2


def test_kubernetes_manifest_contract_round_trip():
    request = deployment_pb2.KubernetesDeployment(
        namespace="codefly",
        destination="/tmp/manifests",
        profile=deployment_pb2.KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
        secret_references={
            "DATABASE_PASSWORD": deployment_pb2.KubernetesSecretKeyReference(
                name="service-secrets",
                key="database-password",
            )
        },
        validate_server_side=True,
        build_context=docker_pb2.DockerBuildContext(
            docker_repository="registry.example.com",
            image_digest="sha256:" + "a" * 64,
        ),
    )
    decoded_request = deployment_pb2.KubernetesDeployment.FromString(
        request.SerializeToString()
    )

    assert (
        decoded_request.profile
        == deployment_pb2.KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1
    )
    assert decoded_request.secret_references["DATABASE_PASSWORD"].key == "database-password"
    assert decoded_request.validate_server_side
    assert decoded_request.build_context.image_digest == "sha256:" + "a" * 64

    output = deployment_pb2.KubernetesDeploymentOutput(
        profile=deployment_pb2.KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1,
        contract_version="codefly.dev/kubernetes-manifest/v1",
        validation=deployment_pb2.KubernetesManifestValidation(
            static_validation=deployment_pb2.KubernetesManifestValidation.STATUS_PASSED,
            server_side_validation=deployment_pb2.KubernetesManifestValidation.STATUS_PASSED,
            promotable=True,
        ),
    )
    decoded_output = deployment_pb2.KubernetesDeploymentOutput.FromString(
        output.SerializeToString()
    )

    assert decoded_output.contract_version == "codefly.dev/kubernetes-manifest/v1"
    assert decoded_output.validation.promotable
