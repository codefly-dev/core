import hashlib
import unittest
from concurrent import futures

import grpc

from codefly.services.agent.v0 import agent_pb2, agent_pb2_grpc, inputs_pb2
from codefly.base.v0 import artifact_execution_pb2
from codefly.services.builder.v0 import builder_pb2
from codefly.services.solution.v0 import solution_pb2


class EffectiveInputsContractTest(unittest.TestCase):
    def test_artifact_execution_identity_matches_go(self):
        digest = "sha256:" + "a" * 64
        execution = artifact_execution_pb2.ArtifactExecution(
            contract_version="artifact-execution/v1",
            selection_identity=digest,
            target="modules/app",
            service="api",
            protocol="codefly.builder.deploy/v1",
            executor_digest=digest,
            configuration_identity="hmac-" + digest,
            binding_identity=digest,
            inputs=[artifact_execution_pb2.ArtifactExecutionInput(
                name="runtime", target="modules/app", artifact="runtime",
                uri="https://artifacts.example.test/runtime",
                media_type="application/octet-stream", digest=digest,
            )],
            outputs=[artifact_execution_pb2.ArtifactExecutionOutput(
                name="manifests", media_type="application/yaml",
            )],
        )
        identity = "sha256:" + hashlib.sha256(
            execution.SerializeToString(deterministic=True)
        ).hexdigest()
        self.assertEqual(
            identity,
            "sha256:2f4abd015ad1cdee13c688e50b06e7f55b0fbf7f155e7bdd886bd71e7d8a039d",
        )
        execution.identity = identity
        for request_type in [builder_pb2.BuildRequest, builder_pb2.DeploymentRequest,
                             solution_pb2.RenderRequest]:
            request = request_type(execution=execution)
            restored = request_type.FromString(request.SerializeToString())
            self.assertEqual(restored.execution, execution)
        receipt = artifact_execution_pb2.ArtifactExecutionReceipt(
            identity=identity,
            outputs=[artifact_execution_pb2.ArtifactExecutionOutput(
                name="manifests", media_type="application/yaml",
                digest=digest, path="manifests.yaml",
            )],
        )
        for response_type in [builder_pb2.BuildResponse, builder_pb2.DeploymentResponse,
                              solution_pb2.RenderResponse]:
            response = response_type(execution=receipt)
            restored = response_type.FromString(response.SerializeToString())
            self.assertEqual(restored.execution, receipt)
        self.assertEqual(list(builder_pb2.BuildCapabilitiesResponse().execution_contracts), [])
        self.assertEqual(list(solution_pb2.SolutionCapabilities().execution_contracts), [])

    def test_go_agent_contract_wire(self):
        wire = bytes.fromhex(
            "62210801121b636f6e7461696e65722d7265636f766572792d73636f70652f76311802"
        )
        declaration = agent_pb2.AgentContract(
            protocol_version=1,
            startup_protocol_version=2,
            capabilities=["container-recovery-scope/v1"],
        )
        message = agent_pb2.AgentInformation(contract=declaration)
        self.assertEqual(message.SerializeToString(), wire)
        self.assertEqual(agent_pb2.AgentInformation.FromString(wire), message)

    def test_published_runtime_requirements_api(self):
        self.assertEqual(agent_pb2.Runtime.NIX, 8)
        self.assertEqual(agent_pb2.Runtime.RUST, 9)
        self.assertEqual(agent_pb2.Runtime.CARGO, 10)
        message = agent_pb2.AgentInformation(runtime_requirements=[
            agent_pb2.Runtime(type=agent_pb2.Runtime.GO, version="1.27")
        ])
        # Serialized by the SDK on the base branch, before input discovery.
        published_wire = bytes.fromhex("0a0808011204312e3237")
        self.assertEqual(message.SerializeToString(), published_wire)
        self.assertEqual(
            agent_pb2.AgentInformation.FromString(published_wire), message
        )

    def test_v1_identity_matches_go(self):
        task = inputs_pb2.TaskInputs(
            task=inputs_pb2.TaskKey(phase=inputs_pb2.TASK_PHASE_ARTIFACT_BUILD),
            complete=True,
        )
        for kind, name, content in [
            (inputs_pb2.EFFECTIVE_INPUT_KIND_TOOLCHAIN, "toolchain", b"resolved-toolchain"),
            (inputs_pb2.EFFECTIVE_INPUT_KIND_PLUGIN, "agent", b"resolved-agent"),
        ]:
            task.inputs.add(
                kind=kind,
                owner="frontend",
                name=name,
                identity=inputs_pb2.EffectiveIdentity(
                    kind=inputs_pb2.EFFECTIVE_IDENTITY_KIND_SHA256,
                    digest=hashlib.sha256(content).hexdigest(),
                ),
            )
        digest = hashlib.sha256(
            b"codefly.effective-inputs/v1\0"
            + task.SerializeToString(deterministic=True)
        ).hexdigest()
        self.assertEqual(
            digest, "cf974b884f3095d736ba2b81da97774a2d25d19999b2ad039592d6f32cd873a2"
        )

    def test_discovery_rpc(self):
        class Agent(agent_pb2_grpc.AgentServicer):
            def GetEffectiveInputs(self, request, context):
                return inputs_pb2.GetEffectiveInputsResponse(
                    schema_version=request.schema_version,
                    snapshot=request.snapshot,
                )

        with futures.ThreadPoolExecutor(max_workers=1) as executor:
            server = grpc.server(executor)
            agent_pb2_grpc.add_AgentServicer_to_server(Agent(), server)
            port = server.add_insecure_port("127.0.0.1:0")
            server.start()
            try:
                with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                    response = agent_pb2_grpc.AgentStub(channel).GetEffectiveInputs(
                        inputs_pb2.GetEffectiveInputsRequest(
                            schema_version=1, snapshot="candidate"
                        ),
                        timeout=5,
                    )
                self.assertEqual(response.schema_version, 1)
                self.assertEqual(response.snapshot, "candidate")
            finally:
                server.stop(0).wait()


if __name__ == "__main__":
    unittest.main()
