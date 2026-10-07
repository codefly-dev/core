import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from codefly.services.builder.v0 import deployment_pb2
from codefly.services.builder.v0 import docker_pb2


# A unittest.TestCase, not bare functions: `python -m unittest discover`
# collects only TestCase classes, so the previous bare-function version of this
# file was imported by CI and never executed — its assertions on the deleted
# profile and field passed by never running.
class DeploymentContractTest(unittest.TestCase):
    def test_kubernetes_manifest_contract_round_trip(self):
        request = deployment_pb2.KubernetesDeployment(
            namespace="codefly",
            destination="/tmp/manifests",
            profile=deployment_pb2.KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
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

        self.assertEqual(
            decoded_request.profile,
            deployment_pb2.KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
        )
        self.assertEqual(
            decoded_request.secret_references["DATABASE_PASSWORD"].key, "database-password"
        )
        self.assertTrue(decoded_request.validate_server_side)
        self.assertEqual(decoded_request.build_context.image_digest, "sha256:" + "a" * 64)

        output = deployment_pb2.KubernetesDeploymentOutput(
            profile=deployment_pb2.KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
            contract_version="codefly.dev/kubernetes-manifest/v1",
            validation=deployment_pb2.KubernetesManifestValidation(
                static_validation=deployment_pb2.KubernetesManifestValidation.STATUS_PASSED,
                server_side_validation=deployment_pb2.KubernetesManifestValidation.STATUS_PASSED,
                restricted=True,
            ),
        )
        decoded_output = deployment_pb2.KubernetesDeploymentOutput.FromString(
            output.SerializeToString()
        )

        self.assertEqual(decoded_output.contract_version, "codefly.dev/kubernetes-manifest/v1")
        self.assertTrue(decoded_output.validation.restricted)

    def test_deleted_profile_and_field_are_gone_from_the_binding(self):
        # KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1 (enum value 2) and
        # KubernetesManifestValidation.promotable (field 3) are deleted from
        # the schema with their numbers and names reserved. The binding is
        # generated from that schema, so it names neither: a stale binding
        # that still did would let a Python caller send a profile every
        # renderer refuses.
        self.assertFalse(hasattr(deployment_pb2, "KUBERNETES_OUTPUT_PROFILE_PROMOTABLE_GITOPS_V1"))
        profile = deployment_pb2.KubernetesOutputProfile
        self.assertEqual(profile.Value("KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1"), 3)
        self.assertNotIn(2, profile.values())
        self.assertEqual(sorted(profile.values()), [0, 1, 3])

        validation = deployment_pb2.KubernetesManifestValidation.DESCRIPTOR
        self.assertNotIn("promotable", validation.fields_by_name)
        self.assertNotIn(3, validation.fields_by_number)
        self.assertIn("restricted", validation.fields_by_name)
        self.assertEqual(validation.fields_by_name["restricted"].number, 6)


if __name__ == "__main__":
    unittest.main()
